package composio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Proxy is the per-project MCP endpoint an agent points at.
//
// One static URL per project, so the ManagedAgent claim stays a normal
// RemoteMCPServer in git: the per-end-user part -- which Composio session, whose
// Gmail -- is resolved here from a request header the agent replays, not from the
// manifest. A per-user URL in git would mean a CR per user in one shared
// cluster-global namespace, which is a name fight rather than a design.
//
// What this layer buys back: over MCP the client executes against Composio
// directly, so the SDK's before/after-execute modifiers never run. Audit, the
// fail-closed identity check, and the toolkit gate exist only because the call
// passes through here.
type Proxy struct {
	svc  *Service
	http *http.Client
}

// NewProxy returns a Proxy over svc.
func NewProxy(svc *Service) *Proxy {
	return &Proxy{svc: svc, http: &http.Client{Timeout: 120 * time.Second}}
}

// jsonRPCRequest is the part of an MCP message the broker needs to read: the
// method, the id to answer with, and the tool name of a call.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  struct {
		Name string `json:"name"`
	} `json:"params"`
}

// ServeMCP proxies one MCP message for one project.
//
// A call with no end-user header is refused rather than served: the broker would
// have to pick a session, and picking any session hands one user another user's
// account. The refusal is a JSON-RPC error, not a transport failure, because a
// 401 reads to an agent as "the tool server is down" while an in-band error
// tells it what is actually missing.
//
// An audit write that fails does not swallow the user's call, but it is logged
// as the defect it is: a tool call that ran unrecorded is invisible spend.
func (p *Proxy) ServeMCP(w http.ResponseWriter, r *http.Request, projectID uuid.UUID) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "cannot read request", http.StatusBadRequest)
		return
	}

	var msg jsonRPCRequest
	_ = json.Unmarshal(body, &msg)

	endUser := strings.TrimSpace(r.Header.Get(EndUserHeader))
	if endUser == "" {
		p.writeRPCError(w, msg.ID, -32001, fmt.Sprintf(
			"no end user on this call: the agent must replay the %s header (allowedHeaders) so the platform knows whose connected accounts to use",
			EndUserHeader))
		return
	}

	row, err := p.svc.store.Session(r.Context(), projectID, endUser)
	if err == ErrNoSession {
		p.writeRPCError(w, msg.ID, -32002,
			"this user has not connected any app yet: open Integrations in the console and authorize one, then retry")
		return
	}
	if err != nil {
		p.writeRPCError(w, msg.ID, -32003, "integration store is unavailable")
		return
	}

	if msg.Method == "tools/call" && msg.Params.Name != "" {
		agent := strings.TrimSpace(r.Header.Get(AgentHeader))
		if err := p.svc.store.RecordToolCall(r.Context(), projectID, endUser, agent, msg.Params.Name); err != nil {
			log.Error().Err(err).
				Str("project", projectID.String()).
				Str("end_user", endUser).
				Str("tool", msg.Params.Name).
				Msg("composio: tool call not recorded, spend is unaudited")
		}
	}

	upstream, err := p.forward(r.Context(), row.MCPURL, body, r.Header.Get("Accept"))
	if err != nil {
		p.writeRPCError(w, msg.ID, -32004, "tool service is unreachable")
		return
	}
	defer upstream.Body.Close()

	_ = p.svc.store.TouchSession(r.Context(), projectID, endUser)
	p.relay(w, upstream)
}

// relay copies the upstream answer verbatim and flushes as it goes: Composio
// answers streamable-HTTP MCP as text/event-stream, and buffering it would stall
// every long tool call until completion.
func (p *Proxy) relay(w http.ResponseWriter, upstream *http.Response) {
	for _, key := range []string{"Content-Type", "Cache-Control", "Mcp-Session-Id"} {
		if v := upstream.Header.Get(key); v != "" {
			w.Header().Set(key, v)
		}
	}
	w.WriteHeader(upstream.StatusCode)
	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, readErr := upstream.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}

// forward posts the message to the end user's Composio session, adding the
// platform key. The key never leaves this process: an agent holds only the
// broker URL.
func (p *Proxy) forward(ctx context.Context, mcpURL string, body []byte, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if accept == "" {
		accept = "application/json, text/event-stream"
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("x-api-key", p.svc.client.apiKey)
	return p.http.Do(req)
}

// writeRPCError answers in-band so the agent reads a reason rather than a dead
// transport. HTTP stays 200 because a JSON-RPC error is a valid MCP response.
func (p *Proxy) writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(payload)
}

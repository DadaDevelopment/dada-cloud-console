package tggateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// A2AClient sends a text message to an agent and returns its reply text.
// Interface so pollers can be tested against an httptest fake instead of a
// real kagent Agent (no A2A reference implementation exists in this repo to
// verify against; this is a JSON-RPC 2.0 message/send client written against
// the A2A protocol's public spec, not confirmed against a live kagent Agent).
type A2AClient interface {
	Send(ctx context.Context, agentName string, text string) (reply string, err error)
	SendWithContext(ctx context.Context, agentName string, contextID string, text string) (reply string, err error)
}

// a2aURLFor derives an agent's A2A endpoint the same way the hand-wired
// telemost-bot's AGENT_A2A_URL env var does.
func a2aURLFor(agentName string) string {
	return fmt.Sprintf("http://%s.kagent.svc.cluster.local:8080", agentName)
}

type httpA2AClient struct {
	http *http.Client
}

// a2aHTTPTimeout bounds one agent round trip; pollers apply their own
// retry/backoff on top of this.
const a2aHTTPTimeout = 90 * time.Second

// NewA2AClient builds an A2AClient that posts JSON-RPC 2.0 message/send
// requests directly to each agent's derived cluster-internal URL.
func NewA2AClient() A2AClient {
	return &httpA2AClient{http: &http.Client{Timeout: a2aHTTPTimeout}}
}

type a2aPart struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

type a2aMessage struct {
	Role      string    `json:"role"`
	MessageID string    `json:"messageId"`
	ContextID string    `json:"contextId,omitempty"`
	Parts     []a2aPart `json:"parts"`
}

type a2aRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  struct {
		Message a2aMessage `json:"message"`
	} `json:"params"`
}

type a2aRPCError struct {
	Message string `json:"message"`
}

type a2aResponse struct {
	Error  *a2aRPCError    `json:"error"`
	Result json.RawMessage `json:"result"`
}

func (c *httpA2AClient) Send(ctx context.Context, agentName string, text string) (string, error) {
	return c.SendWithContext(ctx, agentName, "", text)
}

// SendWithContext posts message/send with an explicit A2A contextId. The A2A
// context is the server-side conversation: the agent (kagent session store)
// keeps the history per contextId, so a stable id per Telegram chat gives the
// model the whole dialogue instead of treating every message as a fresh
// start. Send() (empty contextID) keeps the legacy stateless behavior --
// each call gets a new server-generated context.
func (c *httpA2AClient) SendWithContext(ctx context.Context, agentName string, contextID string, text string) (string, error) {
	reqBody := a2aRequest{JSONRPC: "2.0", ID: "tg-gateway", Method: "message/send"}
	reqBody.Params.Message = a2aMessage{Role: "user", MessageID: uuid.NewString(), Parts: []a2aPart{{Kind: "text", Text: text}}}
	if contextID != "" {
		reqBody.Params.Message.ContextID = contextID
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	url := a2aURLFor(agentName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("a2a %s: %w", agentName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("a2a %s: status %d", agentName, resp.StatusCode)
	}

	var parsed a2aResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("a2a %s: decode response: %w", agentName, err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("a2a %s: %s", agentName, parsed.Error.Message)
	}

	if isInputRequired(parsed.Result) {
		log.Warn().Str("agent", agentName).Msg("tggateway: agent paused on input-required (ask_user or similar HITL tool) - this client is one-shot and cannot resume it")
		return a2aInputRequiredFallback, nil
	}

	text = extractText(parsed.Result)
	if text == "" {
		return "", fmt.Errorf("a2a %s: no text in response", agentName)
	}
	return text, nil
}

// a2aInputRequiredFallback is sent to the Telegram user when an agent pauses
// mid-task waiting for a confirmation handshake (e.g. the kagent/ADK
// built-in ask_user tool). This client is a stateless one-shot A2A caller -
// it has no way to resume a paused task the way kagent's own dashboard
// does - so instead of leaving the user with silence or a raw transport
// error, it asks them to rephrase as a single message.
const a2aInputRequiredFallback = "не смог обработать вопрос за один шаг, переформулируйте его одним сообщением"

// isInputRequired reports whether an A2A result is a paused task
// (status.state == "input-required"), which happens when the agent's model
// invokes a human-in-the-loop confirmation tool. Such a response carries no
// "artifacts" and its question text lives in a shape extractText does not
// parse (observed: a "question" field, not a "text" part), so it must be
// detected before falling through to the no-text error path.
func isInputRequired(raw json.RawMessage) bool {
	var v struct {
		Status struct {
			State string `json:"state"`
		} `json:"status"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	return v.Status.State == "input-required"
}

// extractText walks an arbitrary JSON value and concatenates every string
// found under a "text" key, skipping the "history" key. Tolerates schema
// variation across A2A server implementations since no reference payload
// shape exists to pin against. history is skipped because A2A Task.history
// echoes every prior message (including the user's own input) back in the
// response envelope, which would duplicate the user's message into the
// extracted reply.
func extractText(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	var out bytes.Buffer
	collectText(v, &out)
	return out.String()
}

func collectText(v any, out *bytes.Buffer) {
	switch val := v.(type) {
	case map[string]any:
		for key, child := range val {
			if key == "history" {
				continue
			}
			if key == "text" {
				if s, ok := child.(string); ok {
					if out.Len() > 0 {
						out.WriteByte('\n')
					}
					out.WriteString(s)
					continue
				}
			}
			collectText(child, out)
		}
	case []any:
		for _, item := range val {
			collectText(item, out)
		}
	}
}

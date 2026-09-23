package tggateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

// endUserHeader and agentHeader carry caller identity to the agent, which
// replays them onto its MCP calls when its claim lists them in allowedHeaders.
// A shared tool server has no other way to know whose connected account a call
// is for, and the integration broker refuses a call that arrives without them.
const (
	endUserHeader = "x-dada-end-user"
	agentHeader   = "x-dada-agent"
)

// endUserFromContextID turns the A2A contextId back into the platform's end-user
// key. The contextId is already the stable per-chat identity ("tg-chat-<chat
// id>"), so deriving from it keeps one source of truth instead of threading a
// second identity argument through every caller. A contextId in another shape
// yields no identity, and the tool server then refuses rather than guesses.
func endUserFromContextID(contextID string) string {
	const prefix = "tg-chat-"
	if !strings.HasPrefix(contextID, prefix) {
		return ""
	}
	chatID := strings.TrimPrefix(contextID, prefix)
	if chatID == "" {
		return ""
	}
	return "telegram:" + chatID
}

// httpA2AClient posts JSON-RPC message/send to an agent.
//
// endpoint exists so a test can point the client at an httptest server: the
// production derivation is cluster-internal DNS, which no test can resolve, and
// the identity headers this client sets are only observable on a real request.
type httpA2AClient struct {
	http     *http.Client
	endpoint func(agentName string) string
}

// agentURL is the address this client posts to for agentName.
func (c *httpA2AClient) agentURL(agentName string) string {
	if c.endpoint != nil {
		return c.endpoint(agentName)
	}
	return a2aURLFor(agentName)
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
	Role      string         `json:"role"`
	MessageID string         `json:"messageId"`
	ContextID string         `json:"contextId,omitempty"`
	Parts     []a2aPart      `json:"parts"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// A2AMetadata is the structured caller identity that rides in the A2A
// message's metadata field. The runtime maps it onto the trace (Langfuse
// user/session/trace name), which the prompt-text identity line cannot do.
// It travels in the context so the A2AClient interface and its test fakes
// stay untouched.
type A2AMetadata map[string]any

type a2aMetadataKey struct{}

// WithA2AMetadata attaches identity metadata for every A2A send made with ctx.
func WithA2AMetadata(ctx context.Context, meta A2AMetadata) context.Context {
	if len(meta) == 0 {
		return ctx
	}
	return context.WithValue(ctx, a2aMetadataKey{}, meta)
}

func a2aMetadataFrom(ctx context.Context) A2AMetadata {
	meta, _ := ctx.Value(a2aMetadataKey{}).(A2AMetadata)
	return meta
}

// TelegramA2AMetadata builds the metadata for one Telegram sender. Keys are
// the platform's, not Langfuse's, so the runtime owns the mapping.
func TelegramA2AMetadata(u TelegramUpdate) A2AMetadata {
	meta := A2AMetadata{
		"dada.channel": "telegram",
		"dada.chat_id": fmt.Sprintf("%d", u.ChatID),
	}
	if u.UserID != 0 {
		meta["dada.user_id"] = fmt.Sprintf("%d", u.UserID)
	}
	if u.Username != "" {
		meta["dada.username"] = u.Username
	}
	if u.FirstName != "" {
		meta["dada.first_name"] = u.FirstName
	}
	if u.ThreadID != 0 {
		meta["dada.thread_id"] = fmt.Sprintf("%d", u.ThreadID)
	}
	return meta
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
	reqBody.Params.Message = a2aMessage{Role: "user", MessageID: uuid.NewString(), Parts: []a2aPart{{Kind: "text", Text: text}}, Metadata: a2aMetadataFrom(ctx)}
	if contextID != "" {
		reqBody.Params.Message.ContextID = contextID
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	url := c.agentURL(agentName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if endUser := endUserFromContextID(contextID); endUser != "" {
		req.Header.Set(endUserHeader, endUser)
		req.Header.Set(agentHeader, agentName)
	}

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

	// The A2A task carries its own typed outcome in status.state. Reading it
	// replaces content-sniffing entirely: "completed" (or no state field, for
	// minimal servers) means extractText below is safe; "input-required" is
	// the known HITL pause this one-shot client cannot resume; every other
	// state - failed, canceled, rejected, auth-required, unknown, or one the
	// spec adds later - means the task did not produce a reply, and whatever
	// text an artifact carries there (including a leaked upstream error body)
	// must not be extracted as if it were one. This is the fix for the class
	// of bug where a new provider failure shape needed a new substring added
	// to a marker list after the fact: the protocol already says which task
	// states are not a reply, so read that instead of the content.
	var envelope a2aResultEnvelope
	if err := json.Unmarshal(parsed.Result, &envelope); err != nil {
		return "", fmt.Errorf("a2a %s: decode result envelope: %w", agentName, err)
	}
	switch envelope.Status.State {
	case "", "completed":
	case "input-required":
		log.Warn().Str("agent", agentName).Msg("tggateway: agent paused on input-required (ask_user or similar HITL tool) - this client is one-shot and cannot resume it")
		return a2aInputRequiredFallback, nil
	default:
		log.Warn().Str("agent", agentName).Str("state", envelope.Status.State).
			Msg("tggateway: a2a task ended in a non-completed state, not extracting its text")
		return a2aFailureFallback, nil
	}

	text = extractText(parsed.Result)
	if text == "" {
		return "", fmt.Errorf("a2a %s: no text in response", agentName)
	}
	return sanitizeModelReply(text), nil
}

// a2aInputRequiredFallback is sent to the Telegram user when an agent pauses
// mid-task waiting for a confirmation handshake (e.g. the kagent/ADK
// built-in ask_user tool). This client is a stateless one-shot A2A caller -
// it has no way to resume a paused task the way kagent's own dashboard
// does - so instead of leaving the user with silence or a raw transport
// error, it asks them to rephrase as a single message.
const a2aInputRequiredFallback = "не смог обработать вопрос за один шаг, переформулируйте его одним сообщением"

// a2aResultEnvelope reads only the one field every A2A task result carries
// regardless of server: status.state. It is decoded separately from the
// full-text extraction so a non-completed task can be recognized before
// extractText ever walks its artifacts.
type a2aResultEnvelope struct {
	Status struct {
		State string `json:"state"`
	} `json:"status"`
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

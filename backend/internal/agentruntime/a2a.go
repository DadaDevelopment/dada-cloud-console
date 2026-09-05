package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

type httpA2AClient struct {
	http *http.Client
}

func NewA2AClient() A2AClient {
	return &httpA2AClient{
		http: &http.Client{Timeout: 90 * time.Second},
	}
}

type a2aPart struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

type a2aMessage struct {
	ContextID string    `json:"contextId,omitempty"`
	Role      string    `json:"role"`
	MessageID string    `json:"messageId"`
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

func (c *httpA2AClient) Send(ctx context.Context, run AgentRunRequest) (string, error) {
	agentName, messages := run.AgentName, run.Messages
	if len(messages) == 0 {
		return "", fmt.Errorf("no messages to send")
	}

	lastMsg := messages[len(messages)-1]
	if lastMsg.Role != "user" {
		return "", fmt.Errorf("last message must be from user")
	}

	reqBody := a2aRequest{JSONRPC: "2.0", ID: "agentruntime", Method: "message/send"}
	reqBody.Params.Message = a2aMessage{
		ContextID: run.ContextID,
		Role:      "user",
		MessageID: uuid.NewString(),
		Parts:     []a2aPart{{Kind: "text", Text: renderAgentRun(run)}},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("http://%s.kagent.svc.cluster.local:8080", agentName)
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("a2a %s: status %d", agentName, resp.StatusCode)
	}

	var parsed a2aResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("a2a %s: decode response: %w", agentName, err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("a2a %s: %s", agentName, parsed.Error.Message)
	}

	var task struct {
		Status struct {
			State string `json:"state"`
		} `json:"status"`
	}
	if json.Unmarshal(parsed.Result, &task) != nil {
		return "", fmt.Errorf("invalid a2a result")
	}
	if state := task.Status.State; state != "" && state != "completed" && state != "input-required" {
		return "", fmt.Errorf("a2a task did not complete: %s", state)
	}
	text := extractText(parsed.Result)
	if text == "" {
		return "", fmt.Errorf("a2a %s: no text in response", agentName)
	}
	return text, nil
}

func buildContextualMessage(messages []Message) string {
	var buf bytes.Buffer
	now := time.Now().UTC()

	if len(messages) > 1 {
		buf.WriteString("## Previous conversation:\n")
		for i := 0; i < len(messages)-1; i++ {
			buf.WriteString(renderMessage(messages[i], now))
		}
		buf.WriteString("\n")
	}

	buf.WriteString("## Current message:\n")
	last := messages[len(messages)-1]
	buf.WriteString(strings.TrimPrefix(renderMessage(last, now), last.Role+": "))
	// renderMessage prefixes "role: " only for history lines; the current
	// message block already labels the section, so strip the prefix if the
	// helper added it.
	buf.WriteString("\n")

	return buf.String()
}

// renderMessage renders one history line. User messages carry their
// source-sent time in a semantic form ("[sent 22:41 UTC, 3m ago]") rather
// than a bare timestamp, so the model can reason about recency the way a
// human reads a chat backlog. Assistant messages have no source time (the
// platform generated them) and render plain. This is the temporal-awareness
// slice of the harness: idle gaps and batched rapid-fire messages become
// visible to the model without any prompt work.
//
// URL entities (Agent Harness v2, Step 5) render as [link] lines under the
// message: the platform already extracted the URL (and its title when the
// site answered in time), so the model sees the link's subject at a glance
// and decides itself whether to open it.
func renderMessage(m Message, now time.Time) string {
	var sb strings.Builder
	if m.Role != "user" || m.SourceSentAt == nil {
		sb.WriteString(fmt.Sprintf("%s: %s\n", m.Role, m.Content))
	} else {
		sb.WriteString(fmt.Sprintf("user [sent %s, %s ago]: %s\n",
			m.SourceSentAt.UTC().Format("15:04 MST"), humanizeDelay(now.Sub(*m.SourceSentAt)), m.Content))
	}
	for _, e := range m.Entities {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		url, _ := em["url"].(string)
		if url == "" {
			continue
		}
		if title, _ := em["title"].(string); title != "" {
			sb.WriteString(fmt.Sprintf("[link] %s (%s)\n", url, title))
		} else {
			sb.WriteString(fmt.Sprintf("[link] %s\n", url))
		}
	}
	return sb.String()
}

// humanizeDelay rounds an age to one coarse unit -- the model needs "3m ago"
// granularity, not "3m12.4s".
func humanizeDelay(d time.Duration) string {
	switch {
	case d < 0:
		d = 0
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// Only protocol reply parts are customer-visible. Never traverse arbitrary
// metadata, tool results or task history, and prefer artifacts over status text.
func extractText(raw json.RawMessage) string {
	var result struct {
		Role      string    `json:"role"`
		Parts     []a2aPart `json:"parts"`
		Artifacts []struct {
			Parts []a2aPart `json:"parts"`
		} `json:"artifacts"`
		Status struct {
			Message struct {
				Role  string    `json:"role"`
				Parts []a2aPart `json:"parts"`
			} `json:"message"`
		} `json:"status"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return ""
	}
	textParts := func(parts []a2aPart) string {
		var texts []string
		for _, part := range parts {
			if part.Kind == "text" && strings.TrimSpace(part.Text) != "" {
				texts = append(texts, part.Text)
			}
		}
		return strings.Join(texts, "\n")
	}
	var artifacts []string
	for _, artifact := range result.Artifacts {
		if text := textParts(artifact.Parts); text != "" {
			artifacts = append(artifacts, text)
		}
	}
	if len(artifacts) > 0 {
		return strings.Join(artifacts, "\n")
	}
	if result.Role == "agent" {
		return textParts(result.Parts)
	}
	if result.Status.Message.Role == "agent" {
		return textParts(result.Status.Message.Parts)
	}
	return ""
}

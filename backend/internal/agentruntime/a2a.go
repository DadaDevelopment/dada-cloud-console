package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

func (c *httpA2AClient) Send(ctx context.Context, agentName string, messages []Message) (string, error) {
	if len(messages) == 0 {
		return "", fmt.Errorf("no messages to send")
	}

	lastMsg := messages[len(messages)-1]
	if lastMsg.Role != "user" {
		return "", fmt.Errorf("last message must be from user")
	}

	reqBody := a2aRequest{JSONRPC: "2.0", ID: "agentruntime", Method: "message/send"}
	reqBody.Params.Message = a2aMessage{
		Role:      "user",
		MessageID: uuid.NewString(),
		Parts:     []a2aPart{{Kind: "text", Text: buildContextualMessage(messages)}},
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

	text := extractText(parsed.Result)
	if text == "" {
		return "", fmt.Errorf("a2a %s: no text in response", agentName)
	}
	return text, nil
}

func buildContextualMessage(messages []Message) string {
	var buf bytes.Buffer

	if len(messages) > 1 {
		buf.WriteString("## Previous conversation:\n")
		for i := 0; i < len(messages)-1; i++ {
			msg := messages[i]
			buf.WriteString(fmt.Sprintf("%s: %s\n", msg.Role, msg.Content))
		}
		buf.WriteString("\n")
	}

	buf.WriteString("## Current message:\n")
	buf.WriteString(messages[len(messages)-1].Content)

	return buf.String()
}

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

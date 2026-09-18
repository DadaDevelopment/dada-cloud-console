package agentjudge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// LLM answers one prompt with the raw model text.
type LLM interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

// OpenAIChat is an OpenAI-compatible chat completion client used for the judge
// call; the same gateway and key as the agent itself.
type OpenAIChat struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	Stream      bool          `json:"stream"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Thinking    *chatThinking `json:"thinking,omitempty"`
}

type chatThinking struct {
	Type string `json:"type"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *OpenAIChat) Complete(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(chatRequest{Model: c.Model, Messages: []chatMessage{{Role: "user", Content: prompt}}, MaxTokens: 4000, Thinking: &chatThinking{Type: "disabled"}})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("judge llm: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("judge llm: decode: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("judge llm: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("judge llm: no choices")
	}
	choice := parsed.Choices[0]
	if strings.TrimSpace(choice.Message.Content) == "" {
		return "", fmt.Errorf("judge llm: empty content, finish_reason=%s", choice.FinishReason)
	}
	return choice.Message.Content, nil
}

// RenderPrompt fills the turn.md template with the LLM criteria, signals and
// the turn itself.
func (s *Spec) RenderPrompt(t Turn) string {
	var criteria strings.Builder
	for _, c := range s.llmCriteria() {
		kind := "true/false, true = нарушение"
		if c.Type == TypeScore {
			kind = "0-100"
		}
		fmt.Fprintf(&criteria, "- `%s` (%s) %s: %s\n", c.Score, kind, c.Title, strings.TrimSpace(c.Text))
	}
	var signals strings.Builder
	for _, sg := range s.Signals {
		fmt.Fprintf(&signals, "- `%s`: %s\n", sg.ID, strings.TrimSpace(sg.When))
	}
	var history strings.Builder
	for _, e := range t.History {
		fmt.Fprintf(&history, "%s: %s\n", e.Role, strings.TrimSpace(e.Text))
	}
	if history.Len() == 0 {
		history.WriteString("(пусто)\n")
	}
	ctx := strings.TrimSpace(t.Context)
	if ctx == "" {
		ctx = "(пусто)"
	}
	r := strings.NewReplacer(
		"{{criteria}}", criteria.String(),
		"{{signals}}", signals.String(),
		"{{history}}", history.String(),
		"{{context}}", ctx,
		"{{input}}", strings.Join(t.Incoming, "\n"),
		"{{output}}", t.Reply,
	)
	return r.Replace(s.Template)
}

// Verdict is one parsed criterion or signal value from the LLM answer.
type Verdict struct {
	Value float64
	Why   string
}

func extractJSON(text string) string {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return ""
	}
	return text[start : end+1]
}

// ParseVerdicts validates the model answer against the spec: every LLM
// criterion and signal is either absent (skipped, null) or a value of the
// declared type. Anything else is dropped with an error listing the misses.
func (s *Spec) ParseVerdicts(text string) (map[string]Verdict, error) {
	body := extractJSON(text)
	if body == "" {
		return nil, fmt.Errorf("judge answer has no json object")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, fmt.Errorf("judge answer: %w", err)
	}
	out := map[string]Verdict{}
	var problems []string
	for _, c := range s.llmCriteria() {
		item, ok := raw[c.Score]
		if !ok {
			problems = append(problems, c.Score+" missing")
			continue
		}
		var wrapped struct {
			Value json.RawMessage `json:"value"`
			Why   string          `json:"why"`
		}
		value := item
		if err := json.Unmarshal(item, &wrapped); err == nil && wrapped.Value != nil {
			value = wrapped.Value
		}
		if string(value) == "null" {
			continue
		}
		v, err := decodeValue(value, c.Type)
		if err != nil {
			problems = append(problems, c.Score+": "+err.Error())
			continue
		}
		out[c.Score] = Verdict{Value: v, Why: strings.TrimSpace(wrapped.Why)}
	}
	for _, sg := range s.Signals {
		item, ok := raw[sg.ID]
		if !ok || string(item) == "null" {
			continue
		}
		var wrapped struct {
			Value json.RawMessage `json:"value"`
		}
		value := item
		if err := json.Unmarshal(item, &wrapped); err == nil && wrapped.Value != nil {
			value = wrapped.Value
		}
		v, err := decodeValue(value, TypeBool)
		if err != nil {
			problems = append(problems, sg.ID+": "+err.Error())
			continue
		}
		out[sg.ID] = Verdict{Value: v}
	}
	if len(problems) > 0 {
		return out, fmt.Errorf("judge answer: %s", strings.Join(problems, "; "))
	}
	return out, nil
}

func decodeValue(raw json.RawMessage, typ string) (float64, error) {
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		if typ != TypeBool {
			return 0, fmt.Errorf("expected number, got bool")
		}
		if b {
			return 1, nil
		}
		return 0, nil
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err != nil {
		var str string
		if err2 := json.Unmarshal(raw, &str); err2 != nil {
			return 0, fmt.Errorf("unreadable value %s", string(raw))
		}
		switch strings.ToLower(strings.TrimSpace(str)) {
		case "true", "yes", "да":
			n = 1
		case "false", "no", "нет":
			n = 0
		default:
			if _, err := fmt.Sscanf(str, "%g", &n); err != nil {
				return 0, fmt.Errorf("unreadable value %q", str)
			}
		}
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("value is not finite")
	}
	if typ == TypeBool {
		if n != 0 && n != 1 {
			return 0, fmt.Errorf("expected bool, got %g", n)
		}
		return n, nil
	}
	if n < 0 || n > 100 {
		return 0, fmt.Errorf("value %g outside 0-100", n)
	}
	return n, nil
}

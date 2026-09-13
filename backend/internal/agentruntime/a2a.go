package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/turnbudget"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type httpA2AClient struct {
	http       *http.Client
	endpoint   func(agentName string) string
	timeout    time.Duration
	retryPause time.Duration
	retries    int
	pause      func(ctx context.Context, d time.Duration) error
}

// failedTaskRetryPause is the first pause before re-sending a turn whose task
// the agent reported as failed; every further retry doubles it. Those failures
// are almost always the model provider rate-limiting a burst of chats, and a
// single short pause loses to a burst that is still going (P10 eval: 4 of 9
// retries failed again), so the runtime backs off a few times before it gives
// the turn up.
const (
	failedTaskRetryPause = 3 * time.Second
	failedTaskRetries    = 3
)

// endUserHeader and agentHeader carry caller identity to the agent, which
// replays them onto its MCP calls when its claim lists them in allowedHeaders.
// A shared tool server has no other way to know whose account a call is for,
// and the integration broker refuses a call that arrives without them.
const (
	endUserHeader = "x-dada-end-user"
	agentHeader   = "x-dada-agent"
)

const askUserUnavailableAnswer = "Инструмент ask_user в этом канале не работает: клиент этот вопрос не видит и ответить на него не может. Ответь клиенту обычным текстом; если нужно что-то уточнить, задай вопрос в самом ответе."

func NewA2AClient() A2AClient {
	return &httpA2AClient{
		http:       &http.Client{},
		endpoint:   kagentEndpoint,
		timeout:    turnbudget.AgentCall(),
		retryPause: failedTaskRetryPause,
		retries:    failedTaskRetries,
		pause:      pauseFor,
	}
}

func kagentEndpoint(agentName string) string {
	return fmt.Sprintf("http://%s.kagent.svc.cluster.local:8080", agentName)
}

func (c *httpA2AClient) url(agentName string) string {
	if c.endpoint == nil {
		return kagentEndpoint(agentName)
	}
	return c.endpoint(agentName)
}

type a2aPart struct {
	Kind string         `json:"kind"`
	Text string         `json:"text,omitempty"`
	Data map[string]any `json:"data,omitempty"`
}

type a2aMessage struct {
	ContextID string    `json:"contextId,omitempty"`
	TaskID    string    `json:"taskId,omitempty"`
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
	Code    int    `json:"code"`
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
	if lastMsg.Role != "user" && lastMsg.Role != "system" {
		return "", fmt.Errorf("last message must be user or system, got %s", lastMsg.Role)
	}

	message := a2aMessage{
		ContextID: run.ContextID,
		Role:      "user",
		MessageID: uuid.NewString(),
		Parts:     []a2aPart{{Kind: "text", Text: renderAgentRun(run)}},
	}
	result, err := c.call(ctx, run, message)
	if err != nil {
		return "", err
	}
	task := parseTask(result)
	for attempt := 0; task.State == "failed" && attempt < c.retries; attempt++ {
		wait := c.retryPause << attempt
		log.Warn().Str("agent", agentName).Str("context", run.ContextID).Str("task", task.ID).
			Str("error", task.Error).Int("attempt", attempt+1).Dur("pause", wait).
			Msg("agentruntime: agent task failed; retrying the turn")
		if err := c.pause(ctx, wait); err != nil {
			return "", fmt.Errorf("retry after failed task: %w", err)
		}
		message.MessageID = uuid.NewString()
		if result, err = c.call(ctx, run, message); err != nil {
			return "", fmt.Errorf("retry after failed task: %w", err)
		}
		task = parseTask(result)
	}
	if task.State == "input-required" {
		log.Warn().Str("agent", agentName).Str("context", run.ContextID).Str("task", task.ID).
			Strs("questions", task.Questions).
			Msg("agentruntime: agent paused on ask_user; resuming with the unavailable-tool answer")
		answers := make([]map[string]any, 0, len(task.Questions))
		for range task.Questions {
			answers = append(answers, map[string]any{"answer": []string{askUserUnavailableAnswer}})
		}
		result, err = c.call(ctx, run, a2aMessage{
			ContextID: run.ContextID,
			TaskID:    task.ID,
			Role:      "user",
			MessageID: uuid.NewString(),
			Parts: []a2aPart{{Kind: "data", Data: map[string]any{
				"decision_type": "approve", "ask_user_answers": answers,
			}}},
		})
		if err != nil {
			return "", fmt.Errorf("resume after ask_user: %w", err)
		}
		task = parseTask(result)
	}
	if task.State != "" && task.State != "completed" {
		if task.Error != "" {
			return "", fmt.Errorf("a2a task did not complete: %s: %s", task.State, task.Error)
		}
		return "", fmt.Errorf("a2a task did not complete: %s", task.State)
	}
	text := extractText(result)
	if text == "" {
		return "", fmt.Errorf("a2a %s: no text in response", agentName)
	}
	return text, nil
}

func (c *httpA2AClient) call(ctx context.Context, run AgentRunRequest, message a2aMessage) (json.RawMessage, error) {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	agentName := run.AgentName
	reqBody := a2aRequest{JSONRPC: "2.0", ID: "agentruntime", Method: "message/send"}
	reqBody.Params.Message = message
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(agentName), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if run.EndUserKey != "" {
		req.Header.Set(endUserHeader, run.EndUserKey)
		req.Header.Set(agentHeader, agentName)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("a2a %s: %w", agentName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("a2a %s: status %d", agentName, resp.StatusCode)
	}

	var parsed a2aResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("a2a %s: decode response: %w", agentName, err)
	}
	if parsed.Error != nil {
		msg := strings.TrimSpace(parsed.Error.Message)
		if msg == "" {
			msg = "agent returned an error without a message (typically the agent could not reach its MCP tool server)"
		}
		return nil, fmt.Errorf("a2a %s: rpc error %d: %s", agentName, parsed.Error.Code, msg)
	}
	var probe struct {
		Status struct {
			State string `json:"state"`
		} `json:"status"`
	}
	if json.Unmarshal(parsed.Result, &probe) != nil {
		return nil, fmt.Errorf("invalid a2a result")
	}
	return parsed.Result, nil
}

type a2aTask struct {
	ID        string
	State     string
	Error     string
	Questions []string
}

func pauseFor(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseTask(raw json.RawMessage) a2aTask {
	var v struct {
		ID     string `json:"id"`
		Status struct {
			State   string `json:"state"`
			Message struct {
				Parts []a2aPart `json:"parts"`
			} `json:"message"`
		} `json:"status"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return a2aTask{}
	}
	task := a2aTask{ID: v.ID, State: v.Status.State}
	for _, part := range v.Status.Message.Parts {
		if part.Kind == "text" && task.State == "failed" {
			task.Error = strings.TrimSpace(strings.Join([]string{task.Error, strings.TrimSpace(part.Text)}, " "))
			continue
		}
		if part.Kind != "data" || part.Data["name"] != "ask_user" {
			continue
		}
		args, _ := part.Data["args"].(map[string]any)
		questions, _ := args["questions"].([]any)
		for _, q := range questions {
			qm, _ := q.(map[string]any)
			text, _ := qm["question"].(string)
			task.Questions = append(task.Questions, text)
		}
	}
	if task.State == "input-required" && len(task.Questions) == 0 {
		task.Questions = []string{""}
	}
	return task
}

func buildContextualMessage(messages []Message) string {
	var buf bytes.Buffer
	now := time.Now().In(RuntimeLocation())

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
// source-sent time in the runtime zone in a semantic form ("[sent 22:41 MSK,
// 3m ago]") rather than a bare timestamp, so the model can reason about
// recency the way a human reads a chat backlog. Assistant messages have no source time (the
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
			m.SourceSentAt.In(RuntimeLocation()).Format("15:04 MST"), humanizeDelay(now.Sub(*m.SourceSentAt)), m.Content))
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
	for _, a := range m.Attachments {
		am, ok := a.(map[string]any)
		if !ok {
			continue
		}
		sb.WriteString(renderAttachment(am))
	}
	return sb.String()
}

// renderAttachment renders one attachment object as a typed context line.
// The form has social meaning (owner's spec): a voice stays visibly a
// voice, an image stays visibly an image -- with its transcript/description
// when a resolver produced one, and an explicit "unavailable" marker when
// not, never silently substituted text.
func renderAttachment(a map[string]any) string {
	kind, _ := a["kind"].(string)
	switch kind {
	case "voice", "video_note":
		dur := 0
		if d, ok := a["duration_seconds"].(float64); ok {
			dur = int(d)
		}
		if tr, ok := a["transcript"].(string); ok {
			return fmt.Sprintf("[voice %ds]: \"%s\"\n", dur, tr)
		}
		return fmt.Sprintf("[voice %ds]: [transcription unavailable]\n", dur)
	case "image":
		if desc, ok := a["description"].(string); ok {
			return fmt.Sprintf("[image]: %s\n", desc)
		}
		return "[image]: [description unavailable]\n"
	case "document":
		name, _ := a["file_name"].(string)
		if name == "" {
			name = "unnamed"
		}
		return fmt.Sprintf("[document %s]\n", name)
	default:
		return ""
	}
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

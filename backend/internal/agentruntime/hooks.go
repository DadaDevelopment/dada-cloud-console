package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

type Hook struct {
	ID            string
	AgentName     string
	Name          string
	TriggerEvent  string
	TriggerConfig map[string]any
	ActionType    string
	ActionConfig  map[string]any
	Enabled       bool
}

type HookExecutor interface {
	Execute(ctx context.Context, event string, conv Conversation, extra any) error
	ListIdleHooks(ctx context.Context) ([]Hook, error)
}

type hookExecutor struct {
	pool *pgxpool.Pool
	http *http.Client
}

func NewHookExecutor(pool *pgxpool.Pool) HookExecutor {
	return &hookExecutor{
		pool: pool,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

func (h *hookExecutor) Execute(ctx context.Context, event string, conv Conversation, extra any) error {
	hooks, err := h.listHooks(ctx, conv.AgentName, event)
	if err != nil {
		return err
	}

	for _, hook := range hooks {
		if err := h.executeOne(ctx, hook, conv, extra); err != nil {
			log.Warn().Err(err).
				Str("hook", hook.Name).
				Str("agent", conv.AgentName).
				Str("event", event).
				Msg("agentruntime: hook execution failed")
			h.recordExecution(ctx, hook.ID, conv.ID.String(), event, "failed", err.Error(), nil, nil)
			return fmt.Errorf("hook %s failed: %w", hook.Name, err)
		} else {
			h.recordExecution(ctx, hook.ID, conv.ID.String(), event, "success", "", nil, nil)
		}
	}
	return nil
}

func (h *hookExecutor) listHooks(ctx context.Context, agentName, event string) ([]Hook, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT id, agent_name, name, trigger_event, trigger_config, action_type, action_config
		FROM lifecycle_hooks
		WHERE agent_name = $1 AND trigger_event = $2 AND enabled = true
	`, agentName, event)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hooks []Hook
	for rows.Next() {
		var hook Hook
		var triggerJSON, actionJSON []byte
		if err := rows.Scan(&hook.ID, &hook.AgentName, &hook.Name, &hook.TriggerEvent, &triggerJSON, &hook.ActionType, &actionJSON); err != nil {
			return nil, err
		}
		json.Unmarshal(triggerJSON, &hook.TriggerConfig)
		json.Unmarshal(actionJSON, &hook.ActionConfig)
		hooks = append(hooks, hook)
	}
	return hooks, rows.Err()
}

func (h *hookExecutor) executeOne(ctx context.Context, hook Hook, conv Conversation, extra any) error {
	switch hook.ActionType {
	case "http":
		return h.executeHTTP(ctx, hook, conv, extra)
	case "metadata":
		return h.executeMetadata(ctx, hook, conv, extra)
	case "schedule":
		return h.executeSchedule(ctx, hook, conv, extra)
	default:
		return fmt.Errorf("unknown action type: %s", hook.ActionType)
	}
}

func (h *hookExecutor) executeHTTP(ctx context.Context, hook Hook, conv Conversation, extra any) error {
	method, _ := hook.ActionConfig["method"].(string)
	if method == "" {
		method = "POST"
	}

	urlTemplate, _ := hook.ActionConfig["url"].(string)
	if urlTemplate == "" {
		return fmt.Errorf("http action requires url")
	}

	url := h.interpolate(urlTemplate, conv)

	bodyTemplate := hook.ActionConfig["body"]
	bodyJSON, err := json.Marshal(bodyTemplate)
	if err != nil {
		return fmt.Errorf("marshal body template: %w", err)
	}

	body := h.interpolate(string(bodyJSON), conv)

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader([]byte(body)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if headers, ok := hook.ActionConfig["headers"].(map[string]any); ok {
		for k, v := range headers {
			if vs, ok := v.(string); ok {
				req.Header.Set(k, h.interpolate(vs, conv))
			}
		}
	}

	resp, err := h.http.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("http %d", resp.StatusCode)
	}

	if storeConfig, ok := hook.ActionConfig["store_response"].(map[string]any); ok {
		var respData map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}

		metadata := conv.Metadata
		if metadata == nil {
			metadata = make(map[string]any)
		}

		for metaKey, jsonPathRaw := range storeConfig {
			jsonPath, _ := jsonPathRaw.(string)
			value := extractJSONPath(respData, jsonPath)
			parts := strings.SplitN(metaKey, ".", 2)
			if len(parts) == 2 && parts[0] == "conversation" && parts[1] == "metadata" {
				continue
			}
			if strings.HasPrefix(metaKey, "conversation.metadata.") {
				key := strings.TrimPrefix(metaKey, "conversation.metadata.")
				metadata[key] = value
			}
		}

		if _, err := h.pool.Exec(ctx, `
			UPDATE conversations SET metadata = $2, updated_at = NOW() WHERE id = $1
		`, conv.ID, metadata); err != nil {
			return fmt.Errorf("update metadata: %w", err)
		}
	}

	return nil
}

func (h *hookExecutor) executeMetadata(ctx context.Context, hook Hook, conv Conversation, extra any) error {
	metadata := conv.Metadata
	if metadata == nil {
		metadata = make(map[string]any)
	}

	updates, ok := hook.ActionConfig["set"].(map[string]any)
	if !ok {
		return fmt.Errorf("metadata action requires set map")
	}

	for key, valueTemplate := range updates {
		if vs, ok := valueTemplate.(string); ok {
			metadata[key] = h.interpolate(vs, conv)
		} else {
			metadata[key] = valueTemplate
		}
	}

	_, err := h.pool.Exec(ctx, `
		UPDATE conversations SET metadata = $2, updated_at = NOW() WHERE id = $1
	`, conv.ID, metadata)
	return err
}

func (h *hookExecutor) executeSchedule(ctx context.Context, hook Hook, conv Conversation, extra any) error {
	return fmt.Errorf("scheduling is not configured")
}

func (h *hookExecutor) ListIdleHooks(ctx context.Context) ([]Hook, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT id, agent_name, name, trigger_event, trigger_config, action_type, action_config
		FROM lifecycle_hooks
		WHERE trigger_event = 'conversation.idle' AND enabled = true
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hooks []Hook
	for rows.Next() {
		var hook Hook
		var triggerJSON, actionJSON []byte
		if err := rows.Scan(&hook.ID, &hook.AgentName, &hook.Name, &hook.TriggerEvent, &triggerJSON, &hook.ActionType, &actionJSON); err != nil {
			return nil, err
		}
		json.Unmarshal(triggerJSON, &hook.TriggerConfig)
		json.Unmarshal(actionJSON, &hook.ActionConfig)
		hooks = append(hooks, hook)
	}
	return hooks, rows.Err()
}

func (h *hookExecutor) recordExecution(ctx context.Context, hookID, conversationID, event, status, errMsg string, reqData, respData map[string]any) {
	_, _ = h.pool.Exec(ctx, `
		INSERT INTO hook_executions (hook_id, conversation_id, trigger_event, status, error_message, request_data, response_data)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, hookID, conversationID, event, status, errMsg, reqData, respData)
}

var interpolatePattern = regexp.MustCompile(`\{\{\s*([^}]+)\s*\}\}`)

func (h *hookExecutor) interpolate(template string, conv Conversation) string {
	return interpolatePattern.ReplaceAllStringFunc(template, func(match string) string {
		path := strings.TrimSpace(strings.Trim(match, "{}"))
		parts := strings.Split(path, ".")

		if len(parts) < 2 {
			return match
		}

		switch parts[0] {
		case "conversation":
			switch parts[1] {
			case "id":
				return conv.ID.String()
			case "external_id":
				return conv.ExternalID
			case "agent_name":
				return conv.AgentName
			case "channel":
				return conv.Channel
			case "metadata":
				if len(parts) == 3 {
					if val, ok := conv.Metadata[parts[2]]; ok {
						return fmt.Sprintf("%v", val)
					}
				}
			}
		case "actor":
			switch parts[1] {
			case "external_id":
				return conv.ActorExternalID
			case "username":
				return conv.ActorUsername
			case "metadata":
				if len(parts) == 3 {
					if val, ok := conv.ActorMetadata[parts[2]]; ok {
						return fmt.Sprintf("%v", val)
					}
				}
			}
		case "response":
			return match
		}

		return match
	})
}

func extractJSONPath(data map[string]any, path string) any {
	path = strings.TrimPrefix(path, "{{ ")
	path = strings.TrimSuffix(path, " }}")
	path = strings.TrimPrefix(path, "response.")

	parts := strings.Split(path, ".")
	var current any = data

	for _, part := range parts {
		if m, ok := current.(map[string]any); ok {
			current = m[part]
		} else {
			return nil
		}
	}
	return current
}

package dadagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// KPI is one KPI-hypothesis line the agent requires on intent submission.
// IntentRequest is the cloud-side shape of the agent's IntentSubmitRequest.
// CloudPayload rides in the agent's cloud_payload field (agent-side addition).
// TaskType, PrimaryPillar, VisiblePrimitives and KPIHypothesis must use the
// agent's fixed enums; cloud-task dispatch keys off CloudPayload.skill_id, so
// TaskType is a generic placeholder ("feature").
type IntentRequest struct {
	IntentID          string         `json:"intent_id"`
	Summary           string         `json:"summary"`
	TaskType          string         `json:"task_type"`
	Priority          string         `json:"priority"`
	CoreLoopImpact    string         `json:"core_loop_impact"`
	PrimaryPillar     string         `json:"primary_pillar"`
	VisiblePrimitives []string       `json:"visible_primitives"`
	KPIHypothesis     []string       `json:"kpi_hypothesis"`
	CloudPayload      map[string]any `json:"cloud_payload"`
}

// SubmitResult is the trimmed-down response from intent submission.
type SubmitResult struct{ WorkflowID string }

// Client talks to the DadaAgent agentsync + files API, bearer-authed via a
// Keycloak client-credentials TokenSource.
type Client struct {
	baseURL string
	ts      *TokenSource
	hc      *http.Client
}

// New builds a DadaAgent client. Returns nil when unconfigured.
func New(baseURL string, ts *TokenSource) *Client {
	if baseURL == "" || ts == nil {
		return nil
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), ts: ts, hc: &http.Client{Timeout: 120 * time.Second}}
}

func (c *Client) auth(ctx context.Context, req *http.Request) error {
	tok, err := c.ts.Token(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

// SubmitIntent posts a new intent to POST /v1/agentsync/intents.
func (c *Client) SubmitIntent(ctx context.Context, in IntentRequest) (SubmitResult, error) {
	if in.Priority == "" {
		in.Priority = "medium"
	}
	b, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/agentsync/intents", bytes.NewReader(b))
	if err != nil {
		return SubmitResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.auth(ctx, req); err != nil {
		return SubmitResult{}, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("dadagent submit: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return SubmitResult{}, fmt.Errorf("dadagent submit: status %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Workflow struct {
			WorkflowID string `json:"workflow_id"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return SubmitResult{}, err
	}
	return SubmitResult{WorkflowID: out.Workflow.WorkflowID}, nil
}

// AutofixRequest is the cloud-side shape of the agent's AutofixBody: launch a
// run that root-causes a reported crash/500/alert and opens a PR.
type AutofixRequest struct {
	RepoFullName  string `json:"repo_full_name"`
	InstallToken  string `json:"install_token"`
	Error         string `json:"error"`
	DefaultBranch string `json:"default_branch,omitempty"`
	Agent         string `json:"agent,omitempty"`
	CallbackURL   string `json:"callback_url,omitempty"`
	Logs          string `json:"logs,omitempty"`
}

// AutofixResult is the trimmed-down response from POST /v1/runs/autofix.
type AutofixResult struct {
	RunID  string
	Status string
}

// Autofix launches an auto-fix run at POST /v1/runs/autofix.
func (c *Client) Autofix(ctx context.Context, in AutofixRequest) (AutofixResult, error) {
	b, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/runs/autofix", bytes.NewReader(b))
	if err != nil {
		return AutofixResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.auth(ctx, req); err != nil {
		return AutofixResult{}, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return AutofixResult{}, fmt.Errorf("dadagent autofix: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return AutofixResult{}, fmt.Errorf("dadagent autofix: status %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return AutofixResult{}, err
	}
	return AutofixResult{RunID: out.RunID, Status: out.Status}, nil
}

// RunInfo is the trimmed-down response from GET /v1/runs/{run_id}: just enough
// to learn the agent-assigned cloud_task_id used for webhook correlation.
type RunInfo struct {
	CloudTaskID string
	Status      string
}

// GetRun reads back a run at GET /v1/runs/{run_id}. Used right after Autofix to
// learn the run's agent-assigned cloud_task_id: the callback webhook for a
// runs-based launch carries cloud_task_id (never intent_id), so the caller must
// read it back once to have a key to persist and later match the webhook on.
func (c *Client) GetRun(ctx context.Context, runID string) (RunInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/runs/"+runID, nil)
	if err != nil {
		return RunInfo{}, err
	}
	if err := c.auth(ctx, req); err != nil {
		return RunInfo{}, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return RunInfo{}, fmt.Errorf("dadagent get run: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return RunInfo{}, fmt.Errorf("dadagent get run: status %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		CloudTaskID string `json:"cloud_task_id"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return RunInfo{}, err
	}
	return RunInfo{CloudTaskID: out.CloudTaskID, Status: out.Status}, nil
}

// ExecuteIntent triggers execution at POST /v1/agentsync/intents/{id}/execute.
func (c *Client) ExecuteIntent(ctx context.Context, intentID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/agentsync/intents/"+intentID+"/execute", nil)
	if err != nil {
		return err
	}
	if err := c.auth(ctx, req); err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("dadagent execute: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("dadagent execute: status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// GetFile proxies an artifact byte stream from the agent. Caller closes the reader.
func (c *Client) GetFile(ctx context.Context, fileID string) (io.ReadCloser, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/files/"+fileID, nil)
	if err != nil {
		return nil, "", err
	}
	if err := c.auth(ctx, req); err != nil {
		return nil, "", err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("dadagent getfile: %w", err)
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, "", fmt.Errorf("dadagent getfile: status %d", resp.StatusCode)
	}
	return resp.Body, resp.Header.Get("Content-Type"), nil
}

// Package mlflow is a thin authenticated proxy in front of the MLflow REST API.
//
// Why proxy: ADR-001 / NFR-002 — the browser never talks directly to platform
// services. The console backend is the only client the MLflow tracking server
// trusts. The proxy filters out registered models whose source falls outside
// the caller project's ai_storage_prefix (D13).
package mlflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is the minimal MLflow REST client used by the AI Studio proxy.
type Client struct {
	baseURL string
	http    *http.Client
	headers http.Header
}

// New constructs a Client. baseURL points at the MLflow tracking server (e.g.
// http://mlflow.mlflow.svc.cluster.local:5000). authHeader (optional) is
// forwarded as-is on every request — supports basic auth or bearer tokens.
func New(baseURL, authHeader string) *Client {
	h := http.Header{}
	if authHeader != "" {
		h.Set("Authorization", authHeader)
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 15 * time.Second},
		headers: h,
	}
}

// RegisteredModel mirrors the relevant subset of MLflow's RegisteredModel.
type RegisteredModel struct {
	Name             string         `json:"name"`
	Description      string         `json:"description,omitempty"`
	LastUpdated      int64          `json:"last_updated_timestamp,omitempty"`
	LatestVersions   []ModelVersion `json:"latest_versions,omitempty"`
}

// ModelVersion mirrors the relevant subset of MLflow's ModelVersion.
type ModelVersion struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Source      string `json:"source"`        // s3://... — the artifact URI
	RunID       string `json:"run_id,omitempty"`
	CurrentStage string `json:"current_stage,omitempty"`
	Status      string `json:"status,omitempty"`
	Description string `json:"description,omitempty"`
}

// SearchRegisteredModels lists registered models whose source/path is inside
// the requested storage prefix. The MLflow API doesn't filter by source, so
// we filter in-process.
func (c *Client) SearchRegisteredModels(ctx context.Context, storagePrefix string, maxResults int) ([]RegisteredModel, error) {
	if maxResults <= 0 {
		maxResults = 100
	}
	q := url.Values{}
	q.Set("max_results", fmt.Sprintf("%d", maxResults))
	body, err := c.do(ctx, "GET", "/api/2.0/mlflow/registered-models/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		RegisteredModels []RegisteredModel `json:"registered_models"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode mlflow response: %w", err)
	}
	if storagePrefix == "" {
		return out.RegisteredModels, nil
	}
	filtered := make([]RegisteredModel, 0, len(out.RegisteredModels))
	for _, rm := range out.RegisteredModels {
		// Keep models that have at least one version whose source is inside the prefix.
		kept := make([]ModelVersion, 0, len(rm.LatestVersions))
		for _, v := range rm.LatestVersions {
			if strings.HasPrefix(v.Source, storagePrefix) {
				kept = append(kept, v)
			}
		}
		if len(kept) == 0 {
			continue
		}
		rm.LatestVersions = kept
		filtered = append(filtered, rm)
	}
	return filtered, nil
}

// GetRegisteredModelVersions returns all versions of a single registered model.
func (c *Client) GetRegisteredModelVersions(ctx context.Context, name string) ([]ModelVersion, error) {
	q := url.Values{}
	q.Set("filter", fmt.Sprintf("name='%s'", strings.ReplaceAll(name, "'", "\\'")))
	q.Set("max_results", "200")
	body, err := c.do(ctx, "GET", "/api/2.0/mlflow/model-versions/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		ModelVersions []ModelVersion `json:"model_versions"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode mlflow response: %w", err)
	}
	return out.ModelVersions, nil
}

// GetModelVersion returns one specific version (used by PinAIModelMlflowVersion render).
func (c *Client) GetModelVersion(ctx context.Context, name, version string) (*ModelVersion, error) {
	q := url.Values{}
	q.Set("name", name)
	q.Set("version", version)
	body, err := c.do(ctx, "GET", "/api/2.0/mlflow/model-versions/get?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		ModelVersion ModelVersion `json:"model_version"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode mlflow response: %w", err)
	}
	return &out.ModelVersion, nil
}

// ErrUnreachable is returned when the MLflow server is not reachable. Callers
// surface a friendly fallback to the user.
var ErrUnreachable = errors.New("mlflow unreachable")

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
	if c.baseURL == "" {
		return nil, ErrUnreachable
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	for k, vs := range c.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("mlflow %s %s: %d %s", method, path, resp.StatusCode, string(rb))
	}
	return rb, nil
}

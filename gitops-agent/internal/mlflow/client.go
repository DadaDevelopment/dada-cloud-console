// Package mlflow is a minimal MLflow REST client for the gitops-agent.
//
// Only one operation is needed here: resolve <name, version> -> source URI
// when the dispatcher is rendering a CreateAIModel from an MLflow pin.
// Browser-side and richer search live in the backend's mlflow package; we
// duplicate the tiny piece we need rather than depend on the backend module.
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

type Client struct {
	baseURL string
	http    *http.Client
	headers http.Header
}

func New(baseURL, authHeader string) *Client {
	if baseURL == "" {
		return nil
	}
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

type modelVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Source  string `json:"source"`
}

// ErrUnreachable is returned when the MLflow server cannot be contacted.
var ErrUnreachable = errors.New("mlflow unreachable")

// GetModelVersionSource returns the artifact URI for one MLflow registered
// model version. Used by the gitops-agent dispatcher to resolve the pin into
// a concrete s3:// URI before rendering the AIModel manifest.
func (c *Client) GetModelVersionSource(ctx context.Context, name, version string) (string, error) {
	if c == nil {
		return "", ErrUnreachable
	}
	q := url.Values{}
	q.Set("name", name)
	q.Set("version", version)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/2.0/mlflow/model-versions/get?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	for k, vs := range c.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("mlflow get %s@%s: %d %s", name, version, resp.StatusCode, string(rb))
	}
	var out struct {
		ModelVersion modelVersion `json:"model_version"`
	}
	if err := json.Unmarshal(rb, &out); err != nil {
		return "", fmt.Errorf("decode mlflow response: %w", err)
	}
	if out.ModelVersion.Source == "" {
		return "", fmt.Errorf("mlflow returned empty source for %s@%s", name, version)
	}
	return out.ModelVersion.Source, nil
}

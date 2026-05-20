package portainer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is a typed HTTP client for the Portainer CE REST API.
type Client struct {
	baseURL    string
	apiToken   string
	httpClient *http.Client
}

// New creates a Portainer API client.
func New(baseURL, apiToken string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		apiToken: apiToken,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.apiToken)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.httpClient.Do(req)
}

func (c *Client) doJSON(ctx context.Context, method, path string, bodyObj, result any) error {
	var bodyReader io.Reader
	if bodyObj != nil {
		b, err := json.Marshal(bodyObj)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	resp, err := c.do(ctx, method, path, bodyReader, "application/json")
	if err != nil {
		return fmt.Errorf("http %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("portainer %s %s: status %d: %s", method, path, resp.StatusCode, string(b))
	}
	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

// CreateEdgeEndpoint registers a new edge environment in Portainer.
// portainerServerURL: "https://portainer.dada.ru" (the Portainer server URL, NOT the agent URL)
// tunnelAddr: "portainer.dada.ru:8000"
func (c *Client) CreateEdgeEndpoint(ctx context.Context, name, portainerServerURL, tunnelAddr string) (*Endpoint, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fields := map[string]string{
		"Name":                    name,
		"EndpointCreationType":    "4",
		"ContainerEngine":         "docker",
		"URL":                     portainerServerURL,
		"EdgeTunnelServerAddress": tunnelAddr,
		"EdgeCheckinInterval":     "15",
		"GroupID":                 "1",
	}
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return nil, err
		}
	}
	w.Close()

	resp, err := c.do(ctx, http.MethodPost, "/api/endpoints", &buf, w.FormDataContentType())
	if err != nil {
		return nil, fmt.Errorf("create edge endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("create edge endpoint status %d: %s", resp.StatusCode, string(b))
	}
	var ep Endpoint
	if err := json.NewDecoder(resp.Body).Decode(&ep); err != nil {
		return nil, fmt.Errorf("decode endpoint response: %w", err)
	}
	return &ep, nil
}

// GetEndpoint fetches endpoint details by ID.
func (c *Client) GetEndpoint(ctx context.Context, id int) (*Endpoint, error) {
	var ep Endpoint
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/api/endpoints/%d", id), nil, &ep); err != nil {
		return nil, err
	}
	return &ep, nil
}

// IsAgentConnected returns true when the edge agent has checked in and is alive.
// ⚠️ Status:1 is NOT sufficient — endpoint is created with Status:1 immediately.
// Must check Heartbeat==true AND LastCheckInDate>0.
func IsAgentConnected(ep *Endpoint) bool {
	return ep.Heartbeat && ep.LastCheckInDate > 0
}

// DeleteEndpoint removes an endpoint from Portainer.
func (c *Client) DeleteEndpoint(ctx context.Context, id int) error {
	resp, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/endpoints/%d", id), nil, "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 404 {
		return fmt.Errorf("delete endpoint %d: status %d", id, resp.StatusCode)
	}
	return nil
}

// ListStacks returns all stacks for a given endpoint.
func (c *Client) ListStacks(ctx context.Context, endpointID int) ([]Stack, error) {
	u := fmt.Sprintf("/api/stacks?filters=%s",
		url.QueryEscape(fmt.Sprintf(`{"EndpointID":%d}`, endpointID)),
	)
	var stacks []Stack
	if err := c.doJSON(ctx, http.MethodGet, u, nil, &stacks); err != nil {
		return nil, err
	}
	return stacks, nil
}

// CreateStackFromGit deploys a Docker Compose stack from a git repository.
// Breaking change Portainer 2.27+: use /api/stacks/create/standalone/repository (NOT old ?method=repository path).
func (c *Client) CreateStackFromGit(ctx context.Context, endpointID int, req CreateStackRequest) (*Stack, error) {
	path := fmt.Sprintf("/api/stacks/create/standalone/repository?endpointId=%d", endpointID)
	var stack Stack
	if err := c.doJSON(ctx, http.MethodPost, path, req, &stack); err != nil {
		return nil, err
	}
	return &stack, nil
}

// RedeployStack triggers a git-pull redeploy on an existing stack.
func (c *Client) RedeployStack(ctx context.Context, stackID, endpointID int, req RedeployStackRequest) error {
	path := fmt.Sprintf("/api/stacks/%d/git/redeploy?endpointId=%d", stackID, endpointID)
	return c.doJSON(ctx, http.MethodPut, path, req, nil)
}

// DeleteStack removes a stack from an endpoint.
func (c *Client) DeleteStack(ctx context.Context, stackID, endpointID int) error {
	path := fmt.Sprintf("/api/stacks/%d?endpointId=%d", stackID, endpointID)
	resp, err := c.do(ctx, http.MethodDelete, path, nil, "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 404 {
		return fmt.Errorf("delete stack %d: status %d", stackID, resp.StatusCode)
	}
	return nil
}

// ListContainers lists containers on an endpoint, filtered by label (e.g. "dada.io/app=myapp").
func (c *Client) ListContainers(ctx context.Context, endpointID int, labelFilter string) ([]Container, error) {
	filter := fmt.Sprintf(`{"label":[%q]}`, labelFilter)
	path := fmt.Sprintf("/api/endpoints/%d/docker/containers/json?filters=%s",
		endpointID, url.QueryEscape(filter))
	var containers []Container
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &containers); err != nil {
		return nil, err
	}
	return containers, nil
}

// StreamLogs returns the raw log stream for a container (caller must close).
// The stream uses Docker's 8-byte multiplexing header per chunk.
func (c *Client) StreamLogs(ctx context.Context, endpointID int, containerID string, tail int) (io.ReadCloser, error) {
	path := fmt.Sprintf("/api/endpoints/%d/docker/containers/%s/logs?stdout=1&stderr=1&follow=1&tail=%s&timestamps=1",
		endpointID, containerID, strconv.Itoa(tail))
	streamClient := &http.Client{} // no timeout for streaming
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.apiToken)
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("stream logs status %d", resp.StatusCode)
	}
	return resp.Body, nil
}

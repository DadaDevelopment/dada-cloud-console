// Package portainer is a lean, read-only client for the Portainer CE REST API,
// used by the console backend to proxy live VM/stack/container state.
//
// It is an intentional, minimal subset of portainer-agent/internal/portainer
// (the agent owns the read-write client). The two are separate Go modules, so
// duplicating the few read methods here keeps the services decoupled.
package portainer

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"encoding/json"
)

// Endpoint is a Portainer environment (a registered VM).
type Endpoint struct {
	ID              int    `json:"Id"`
	Name            string `json:"Name"`
	Status          int    `json:"Status"` // 1 = up, 2 = down (Portainer's view)
	Heartbeat       bool   `json:"Heartbeat"`
	LastCheckInDate int64  `json:"LastCheckInDate"`
}

// Stack is a Portainer stack (a compose workload).
type Stack struct {
	ID         int    `json:"Id"`
	Name       string `json:"Name"`
	EndpointID int    `json:"EndpointId"`
	Status     int    `json:"Status"` // 1 = active, 2 = inactive
}

// Container is a Docker container as returned by the Portainer docker proxy.
type Container struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Labels map[string]string `json:"Labels"`
}

// Client is a read-only Portainer API client.
type Client struct {
	baseURL    string
	apiToken   string
	httpClient *http.Client
}

// New creates a read-only Portainer client. Returns nil if unconfigured so
// callers can treat the live-state feature as disabled.
func New(baseURL, apiToken string) *Client {
	if baseURL == "" || apiToken == "" {
		return nil
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiToken:   apiToken,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) getJSON(ctx context.Context, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", c.apiToken)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("portainer GET %s: status %d: %s", path, resp.StatusCode, string(b))
	}
	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

// GetEndpoint fetches a single endpoint (for heartbeat/connectivity state).
func (c *Client) GetEndpoint(ctx context.Context, endpointID int) (*Endpoint, error) {
	var ep Endpoint
	if err := c.getJSON(ctx, fmt.Sprintf("/api/endpoints/%d", endpointID), &ep); err != nil {
		return nil, err
	}
	return &ep, nil
}

// ListStacks returns all stacks for an endpoint.
func (c *Client) ListStacks(ctx context.Context, endpointID int) ([]Stack, error) {
	u := fmt.Sprintf("/api/stacks?filters=%s",
		url.QueryEscape(fmt.Sprintf(`{"EndpointID":%d}`, endpointID)))
	var stacks []Stack
	if err := c.getJSON(ctx, u, &stacks); err != nil {
		return nil, err
	}
	return stacks, nil
}

// ListContainers lists containers on an endpoint. labelFilter is optional
// (e.g. "com.docker.compose.project=myapp"); empty lists all (incl. stopped).
func (c *Client) ListContainers(ctx context.Context, endpointID int, labelFilter string) ([]Container, error) {
	path := fmt.Sprintf("/api/endpoints/%d/docker/containers/json?all=1", endpointID)
	if labelFilter != "" {
		filter := fmt.Sprintf(`{"label":[%q]}`, labelFilter)
		path += "&filters=" + url.QueryEscape(filter)
	}
	var containers []Container
	if err := c.getJSON(ctx, path, &containers); err != nil {
		return nil, err
	}
	return containers, nil
}

// GetContainerLogs returns the last `tail` lines of a container's logs as plain
// text (non-following). Docker's stream multiplexing headers are stripped.
func (c *Client) GetContainerLogs(ctx context.Context, endpointID int, containerID string, tail int) (string, error) {
	path := fmt.Sprintf("/api/endpoints/%d/docker/containers/%s/logs?stdout=1&stderr=1&tail=%s&timestamps=1",
		endpointID, url.PathEscape(containerID), strconv.Itoa(tail))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-API-Key", c.apiToken)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("logs request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("portainer logs: status %d: %s", resp.StatusCode, string(b))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // cap 1 MiB
	if err != nil {
		return "", err
	}
	return string(demuxDockerStream(raw)), nil
}

// demuxDockerStream strips Docker's 8-byte multiplexing headers from a
// non-TTY log stream. Each frame is [stream(1)][000][size(4 BE)][payload].
// If the data doesn't look framed, it is returned unchanged (TTY streams).
func demuxDockerStream(b []byte) []byte {
	out := make([]byte, 0, len(b))
	i := 0
	for i+8 <= len(b) {
		st := b[i]
		if st > 2 { // not a valid stream type → treat as raw/TTY
			return b
		}
		size := int(binary.BigEndian.Uint32(b[i+4 : i+8]))
		i += 8
		if size < 0 || i+size > len(b) {
			break
		}
		out = append(out, b[i:i+size]...)
		i += size
	}
	if len(out) == 0 {
		return b
	}
	return out
}

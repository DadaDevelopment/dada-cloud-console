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

// FindEndpointByName searches Portainer endpoints by exact name, returns nil if not found.
func (c *Client) FindEndpointByName(ctx context.Context, name string) (*Endpoint, error) {
	var endpoints []Endpoint
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/api/endpoints?search=%s", url.QueryEscape(name)), nil, &endpoints); err != nil {
		return nil, fmt.Errorf("list endpoints: %w", err)
	}
	for i := range endpoints {
		if endpoints[i].Name == name {
			return &endpoints[i], nil
		}
	}
	return nil, nil
}

// CreateEdgeEndpoint registers a new edge environment in Portainer.
// Idempotent: if an endpoint with the same name already exists (409), it is returned.
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

	if resp.StatusCode == http.StatusConflict {
		// Endpoint already exists — look it up by name (idempotent retry).
		existing, findErr := c.FindEndpointByName(ctx, name)
		if findErr != nil {
			return nil, fmt.Errorf("create edge endpoint: name already taken, lookup failed: %w", findErr)
		}
		if existing != nil {
			return existing, nil
		}
		return nil, fmt.Errorf("create edge endpoint: 409 conflict but endpoint '%s' not found", name)
	}

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

// EnsureEdgeCompute enables Portainer's edge-compute features and sets the URL
// edge agents poll for edge-stack updates. Required before /api/edge_groups and
// /api/edge_stacks work (they 503 otherwise). Idempotent. edgePortainerURL is the
// public Portainer URL agents reach (e.g. https://portainer.dada-tuda.ru).
func (c *Client) EnsureEdgeCompute(ctx context.Context, edgePortainerURL string) error {
	body := map[string]any{
		"EnableEdgeComputeFeatures": true,
		"EdgePortainerUrl":          edgePortainerURL,
	}
	return c.doJSON(ctx, http.MethodPut, "/api/settings", body, nil)
}

// EnsureTag returns the id of the tag with the given name, creating it if absent.
func (c *Client) EnsureTag(ctx context.Context, name string) (int, error) {
	var tags []Tag
	if err := c.doJSON(ctx, http.MethodGet, "/api/tags", nil, &tags); err != nil {
		return 0, fmt.Errorf("list tags: %w", err)
	}
	for _, t := range tags {
		if t.Name == name {
			return t.ID, nil
		}
	}
	var created Tag
	if err := c.doJSON(ctx, http.MethodPost, "/api/tags", map[string]string{"name": name}, &created); err != nil {
		return 0, fmt.Errorf("create tag %q: %w", name, err)
	}
	return created.ID, nil
}

// ListEdgeGroups returns all edge groups.
func (c *Client) ListEdgeGroups(ctx context.Context) ([]EdgeGroup, error) {
	var groups []EdgeGroup
	if err := c.doJSON(ctx, http.MethodGet, "/api/edge_groups", nil, &groups); err != nil {
		return nil, err
	}
	return groups, nil
}

// EnsureEdgeGroup returns the dynamic edge group with the given name, creating it
// (matching any endpoint carrying tagID) if absent. Idempotent.
func (c *Client) EnsureEdgeGroup(ctx context.Context, name string, tagID int) (*EdgeGroup, error) {
	groups, err := c.ListEdgeGroups(ctx)
	if err != nil {
		return nil, err
	}
	for i := range groups {
		if groups[i].Name == name {
			return &groups[i], nil
		}
	}
	var created EdgeGroup
	if err := c.doJSON(ctx, http.MethodPost, "/api/edge_groups", createEdgeGroupRequest{
		Name:    name,
		Dynamic: true,
		TagIDs:  []int{tagID},
	}, &created); err != nil {
		return nil, fmt.Errorf("create edge group %q: %w", name, err)
	}
	return &created, nil
}

// TagEndpoint adds tagID to an edge endpoint so a dynamic edge group includes it.
// Idempotent: it merges with the endpoint's existing tags. PUT /api/endpoints/{id}
// with the full TagIDs set is the only supported way to assign endpoint tags.
func (c *Client) TagEndpoint(ctx context.Context, endpointID, tagID int) error {
	ep, err := c.GetEndpoint(ctx, endpointID)
	if err != nil {
		return err
	}
	for _, t := range ep.TagIDs {
		if t == tagID {
			return nil // already tagged
		}
	}
	tags := append(append([]int{}, ep.TagIDs...), tagID)
	return c.doJSON(ctx, http.MethodPut, fmt.Sprintf("/api/endpoints/%d", endpointID),
		map[string]any{"TagIDs": tags}, nil)
}

// ListEdgeStacks returns all edge stacks.
func (c *Client) ListEdgeStacks(ctx context.Context) ([]EdgeStack, error) {
	var stacks []EdgeStack
	if err := c.doJSON(ctx, http.MethodGet, "/api/edge_stacks", nil, &stacks); err != nil {
		return nil, err
	}
	return stacks, nil
}

// CreateEdgeStackFromGit creates a compose edge stack sourced from git, targeting
// the given edge groups. Portainer pulls the compose and pushes it to every
// endpoint in those groups.
func (c *Client) CreateEdgeStackFromGit(ctx context.Context, req CreateEdgeStackGitRequest) (*EdgeStack, error) {
	var stack EdgeStack
	if err := c.doJSON(ctx, http.MethodPost, "/api/edge_stacks/create/repository", req, &stack); err != nil {
		return nil, err
	}
	return &stack, nil
}

// RedeployEdgeStackFromGit triggers a git pull + redeploy of an existing edge
// stack, fanning the latest config to every endpoint in its edge groups. This is
// how a config change reaches ALL VMs (existing included) without SSH.
func (c *Client) RedeployEdgeStackFromGit(ctx context.Context, stackID int) error {
	return c.doJSON(ctx, http.MethodPut, fmt.Sprintf("/api/edge_stacks/%d/git", stackID),
		map[string]any{}, nil)
}

// EnsureEdgeStackFromGit returns the edge stack with the given name, creating it
// from git if absent. Idempotent ensure used by the reconcile loop.
func (c *Client) EnsureEdgeStackFromGit(ctx context.Context, req CreateEdgeStackGitRequest) (*EdgeStack, error) {
	stacks, err := c.ListEdgeStacks(ctx)
	if err != nil {
		return nil, err
	}
	for i := range stacks {
		if stacks[i].Name == req.Name {
			return &stacks[i], nil
		}
	}
	return c.CreateEdgeStackFromGit(ctx, req)
}

// ListContainers lists running containers on an endpoint. When labelFilter is
// non-empty it constrains by label (e.g. "dada.io/app=myapp"); when empty it
// returns ALL running containers. An empty labelFilter must NOT be sent as
// {"label":[""]} — Docker reads that as "a label whose key is the empty string"
// and matches nothing, so the filter param is omitted entirely in that case.
func (c *Client) ListContainers(ctx context.Context, endpointID int, labelFilter string) ([]Container, error) {
	path := fmt.Sprintf("/api/endpoints/%d/docker/containers/json", endpointID)
	if labelFilter != "" {
		filter := fmt.Sprintf(`{"label":[%q]}`, labelFilter)
		path += "?filters=" + url.QueryEscape(filter)
	}
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

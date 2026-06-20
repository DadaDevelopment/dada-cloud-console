// Package registry talks to Harbor: ensure per-project repos and mint robots.
package registry

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

// RobotCreds are the credentials for a minted Harbor robot account.
type RobotCreds struct {
	Name   string
	Secret string
}

// Registry is the Harbor surface build-agent needs. Implemented by Harbor: the
// REST v2.0 API against HARBOR_URL with admin creds.
type Registry interface {
	// EnsureProject lazily creates the per-dada-project Harbor project (slug =
	// projects.name) on first repo link. Idempotent.
	EnsureProject(ctx context.Context, projectSlug string) error
	// MintRobot creates a scoped robot account (role "build" → push+pull, or
	// "deploy" → pull-only) for a project and returns its credentials.
	MintRobot(ctx context.Context, projectSlug, role string) (RobotCreds, error)
	// ImageURI builds the canonical immutable image reference (digest pin).
	ImageURI(projectSlug, appName, digest string) string
	// ImageTag builds the human-readable tag reference (git sha tag).
	ImageTag(projectSlug, appName, tag string) string
	// CacheRef is the registry-backed BuildKit cache tag for a repo (one per app).
	CacheRef(projectSlug, appName string) string
	// Host returns the bare registry host (no scheme), used in image refs.
	Host() string
}

// Harbor is the production Registry, backed by the Harbor REST API.
type Harbor struct {
	baseURL     string // e.g. https://harbor.dada-tuda.ru
	host        string // e.g. harbor.dada-tuda.ru (for image refs)
	adminUser   string
	adminSecret string
	http        *http.Client
}

// NewHarbor returns a Harbor client. baseURL may be given with or without a
// scheme; image refs always use the scheme-less host.
func NewHarbor(baseURL, adminUser, adminSecret string) *Harbor {
	host := baseURL
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimRight(host, "/")

	api := baseURL
	if !strings.HasPrefix(api, "http://") && !strings.HasPrefix(api, "https://") {
		api = "https://" + api
	}
	api = strings.TrimRight(api, "/")

	return &Harbor{
		baseURL:     api,
		host:        host,
		adminUser:   adminUser,
		adminSecret: adminSecret,
		http:        &http.Client{Timeout: 30 * time.Second},
	}
}

func (h *Harbor) Host() string { return h.host }

func (h *Harbor) ImageURI(projectSlug, appName, digest string) string {
	// harbor.dada-tuda.ru/<proj>/<app>@sha256:<digest>
	d := digest
	if !strings.HasPrefix(d, "sha256:") {
		d = "sha256:" + d
	}
	return fmt.Sprintf("%s/%s/%s@%s", h.host, projectSlug, appName, d)
}

func (h *Harbor) ImageTag(projectSlug, appName, tag string) string {
	return fmt.Sprintf("%s/%s/%s:%s", h.host, projectSlug, appName, tag)
}

func (h *Harbor) CacheRef(projectSlug, appName string) string {
	return fmt.Sprintf("%s/%s/%s:buildcache", h.host, projectSlug, appName)
}

// do issues an authenticated JSON request against the Harbor API.
func (h *Harbor) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(h.adminUser, h.adminSecret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	return h.http.Do(req)
}

// EnsureProject creates the Harbor project if it does not exist. Trivy
// scan-on-push, tag immutability, and quotas are configured on the project at
// creation; a 409 (already exists) is treated as success.
func (h *Harbor) EnsureProject(ctx context.Context, projectSlug string) error {
	// Fast path: does the project already exist? HEAD /projects?project_name=.
	headReq, err := http.NewRequestWithContext(ctx, http.MethodHead,
		h.baseURL+"/api/v2.0/projects?project_name="+projectSlug, nil)
	if err != nil {
		return err
	}
	headReq.SetBasicAuth(h.adminUser, h.adminSecret)
	if resp, err := h.http.Do(headReq); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil // already exists
		}
	}

	reqBody := map[string]any{
		"project_name": projectSlug,
		"public":       false,
		"metadata": map[string]string{
			"auto_scan":               "true", // Trivy scan-on-push
			"reuse_sys_cve_allowlist": "true",
		},
	}
	resp, err := h.do(ctx, http.MethodPost, "/api/v2.0/projects", reqBody)
	if err != nil {
		return fmt.Errorf("create harbor project: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK, http.StatusConflict:
		return nil
	default:
		return fmt.Errorf("create harbor project %q: %s", projectSlug, readErr(resp))
	}
}

// robotRequest is the Harbor v2.0 robot-account create payload (project-scoped).
type robotRequest struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Duration    int               `json:"duration"` // -1 = no expiry
	Level       string            `json:"level"`    // "project"
	Permissions []robotPermission `json:"permissions"`
}

type robotPermission struct {
	Kind      string        `json:"kind"`      // "project"
	Namespace string        `json:"namespace"` // project slug
	Access    []robotAccess `json:"access"`
}

type robotAccess struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

type robotResponse struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`   // robot$<proj>+<name>
	Secret string `json:"secret"` // returned exactly once on create
}

// MintRobot creates a project-scoped robot. role "build" → push+pull; "deploy" →
// pull-only.
func (h *Harbor) MintRobot(ctx context.Context, projectSlug, role string) (RobotCreds, error) {
	var access []robotAccess
	switch role {
	case "build":
		access = []robotAccess{
			{Resource: "repository", Action: "push"},
			{Resource: "repository", Action: "pull"},
		}
	case "deploy":
		access = []robotAccess{
			{Resource: "repository", Action: "pull"},
		}
	default:
		return RobotCreds{}, fmt.Errorf("unknown robot role %q", role)
	}

	reqBody := robotRequest{
		Name:        role,
		Description: "build-agent " + role + " robot for " + projectSlug,
		Duration:    -1,
		Level:       "project",
		Permissions: []robotPermission{{
			Kind:      "project",
			Namespace: projectSlug,
			Access:    access,
		}},
	}
	resp, err := h.do(ctx, http.MethodPost, "/api/v2.0/robots", reqBody)
	if err != nil {
		return RobotCreds{}, fmt.Errorf("mint robot: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return RobotCreds{}, fmt.Errorf("mint robot %s/%s: %s", projectSlug, role, readErr(resp))
	}
	var rr robotResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return RobotCreds{}, fmt.Errorf("decode robot: %w", err)
	}
	return RobotCreds{Name: rr.Name, Secret: rr.Secret}, nil
}

// TriggerScan kicks a Trivy scan for a pushed artifact (optional; auto_scan on
// the project already does this on push). Best-effort.
func (h *Harbor) TriggerScan(ctx context.Context, projectSlug, appName, digest string) error {
	d := digest
	if !strings.HasPrefix(d, "sha256:") {
		d = "sha256:" + d
	}
	path := fmt.Sprintf("/api/v2.0/projects/%s/repositories/%s/artifacts/%s/scan",
		projectSlug, appName, d)
	resp, err := h.do(ctx, http.MethodPost, path, map[string]string{})
	if err != nil {
		return fmt.Errorf("trigger scan: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("trigger scan: %s", readErr(resp))
	}
	return nil
}

func readErr(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	return fmt.Sprintf("%d %s", resp.StatusCode, strings.TrimSpace(string(b)))
}

var _ Registry = (*Harbor)(nil)

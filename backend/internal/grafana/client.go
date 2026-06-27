// Package grafana is a lean client for the Grafana HTTP API used by the console
// to provision (and tear down) the Grafana-native pieces of DADA Monitoring:
// per-project folders, per-resource dashboards, alert rules, and contact points
// (Telegram / Email / Webhook). Grafana owns alert evaluation, routing, dedup and
// silencing — we only CRUD the objects and pull the firing-alert state back for
// the health badge. See ADR-011.
//
// Multi-tenant isolation (ADR-011, consequences): a folder is created per project
// and dashboards/alerts live inside it. Full per-customer isolation in Grafana OSS
// requires Grafana Orgs (or Enterprise RBAC); with a shared org we scope by folder
// + org_id/project_id labels and (best-effort) strip the default Editor/Viewer
// role from each folder so customers reach dashboards only via the backend-issued
// deep link, never the shared Grafana UI. SetFolderTenant encapsulates that.
package grafana

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to one Grafana instance, authenticating with EITHER admin
// basic-auth (preferred) OR a service-account / admin API token.
//
// Auth choice matters for durability. The shared Grafana runs on emptyDir, so a
// pod restart wipes its DB — including every service-account token (the SA token
// lives in the DB). Admin basic-auth, by contrast, is reconstituted from the
// chart-managed Secret (GF_SECURITY_ADMIN_USER/PASSWORD env) on every boot, so it
// survives the wipe and the console keeps provisioning without a manual token
// re-mint. Prefer NewBasicAuth; the token constructor (New) remains for
// environments with persistent Grafana storage. See ADR-011.
type Client struct {
	baseURL    string // API base, no trailing slash
	publicURL  string // browser-facing base for deep links (may differ from baseURL)
	apiToken   string // Bearer token; empty when basic-auth is used
	user, pass string // admin basic-auth; empty when token is used
	promDSUID  string // Prometheus datasource UID alert rules query against
	httpClient *http.Client
}

// New returns a token-authenticated Grafana client, or nil when unconfigured so
// callers can treat all alerting/dashboard provisioning as disabled (handlers
// respond 503). publicURL falls back to baseURL when empty.
func New(baseURL, apiToken, promDatasourceUID, publicURL string) *Client {
	if baseURL == "" || apiToken == "" {
		return nil
	}
	c := newClient(baseURL, promDatasourceUID, publicURL)
	c.apiToken = apiToken
	return c
}

// NewBasicAuth returns an admin-basic-auth Grafana client, or nil when
// unconfigured. This is the durable auth path for emptyDir-backed Grafana: the
// admin credential is re-provisioned from env on every Grafana boot, so it
// outlives a DB wipe that would invalidate a service-account token.
func NewBasicAuth(baseURL, user, pass, promDatasourceUID, publicURL string) *Client {
	if baseURL == "" || user == "" || pass == "" {
		return nil
	}
	c := newClient(baseURL, promDatasourceUID, publicURL)
	c.user, c.pass = user, pass
	return c
}

// newClient builds the shared parts of a Client (URLs, http client). The caller
// sets exactly one auth mode (apiToken OR user/pass) afterwards.
func newClient(baseURL, promDatasourceUID, publicURL string) *Client {
	base := strings.TrimRight(baseURL, "/")
	pub := strings.TrimRight(publicURL, "/")
	if pub == "" {
		pub = base
	}
	return &Client{
		baseURL:    base,
		publicURL:  pub,
		promDSUID:  promDatasourceUID,
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

// PublicURL is the browser-facing Grafana base (for building deep links).
func (c *Client) PublicURL() string { return c.publicURL }

// do issues a JSON request. provenanceless=true sends X-Disable-Provenance so
// provisioning-API objects stay editable in the Grafana UI. out may be nil.
// Returns the HTTP status so callers can branch on 404 (exists checks).
func (c *Client) do(ctx context.Context, method, path string, body, out any, provenanceless bool) (int, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return 0, err
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if provenanceless {
		req.Header.Set("X-Disable-Provenance", "true")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("grafana %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return resp.StatusCode, fmt.Errorf("grafana %s %s: status %d: %s", method, path, resp.StatusCode, string(raw))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("grafana %s %s: decode: %w", method, path, err)
		}
	}
	return resp.StatusCode, nil
}

// ---- Folders (per project) -------------------------------------------------

// EnsureFolder creates the project folder if absent (idempotent). uid must be a
// stable, deterministic per-project id so we never need a lookup table.
func (c *Client) EnsureFolder(ctx context.Context, uid, title string) error {
	status, err := c.do(ctx, http.MethodGet, "/api/folders/"+uid, nil, nil, false)
	if err == nil {
		return nil // exists
	}
	if status != http.StatusNotFound {
		return err
	}
	body := map[string]any{"uid": uid, "title": title}
	_, err = c.do(ctx, http.MethodPost, "/api/folders", body, nil, false)
	// Another request may have created it concurrently → treat 409/412 as success.
	if err != nil && !strings.Contains(err.Error(), "status 409") && !strings.Contains(err.Error(), "status 412") {
		return err
	}
	return nil
}

// SetFolderTenant best-effort removes inherited Editor/Viewer access so the
// folder is reachable only via the backend deep link (shared-org isolation).
// Grafana OSS accepts a permissions list of {role, permission}; passing only the
// service account's implicit admin leaves no broad role. Errors are non-fatal
// (older Grafana / Enterprise RBAC may reject); caller logs and continues.
func (c *Client) SetFolderTenant(ctx context.Context, folderUID string) error {
	body := map[string]any{"items": []any{}} // empty = revoke inherited role grants
	_, err := c.do(ctx, http.MethodPost, "/api/folders/"+folderUID+"/permissions", body, nil, false)
	return err
}

// ---- Dashboards (per resource) ---------------------------------------------

// UpsertDashboard creates or overwrites a dashboard inside a folder. dashboard
// is the full Grafana dashboard model (must contain a stable "uid").
func (c *Client) UpsertDashboard(ctx context.Context, folderUID string, dashboard map[string]any) error {
	body := map[string]any{
		"dashboard": dashboard,
		"folderUid": folderUID,
		"overwrite": true,
	}
	_, err := c.do(ctx, http.MethodPost, "/api/dashboards/db", body, nil, false)
	return err
}

// DeleteDashboard removes a dashboard by uid. Missing is not an error.
func (c *Client) DeleteDashboard(ctx context.Context, uid string) error {
	status, err := c.do(ctx, http.MethodDelete, "/api/dashboards/uid/"+uid, nil, nil, false)
	if status == http.StatusNotFound {
		return nil
	}
	return err
}

// ---- Contact points (channels) ---------------------------------------------

// ContactPoint is the provisioning representation of a Grafana contact point.
type ContactPoint struct {
	UID                   string         `json:"uid,omitempty"`
	Name                  string         `json:"name"`
	Type                  string         `json:"type"`     // telegram | email | webhook
	Settings              map[string]any `json:"settings"` // type-specific
	DisableResolveMessage bool           `json:"disableResolveMessage,omitempty"`
}

// CreateContactPoint provisions a contact point and returns its uid.
func (c *Client) CreateContactPoint(ctx context.Context, cp ContactPoint) (string, error) {
	var out ContactPoint
	if _, err := c.do(ctx, http.MethodPost, "/api/v1/provisioning/contact-points", cp, &out, true); err != nil {
		return "", err
	}
	return out.UID, nil
}

// DeleteContactPoint removes a contact point by uid. Missing is not an error.
func (c *Client) DeleteContactPoint(ctx context.Context, uid string) error {
	if uid == "" {
		return nil
	}
	status, err := c.do(ctx, http.MethodDelete, "/api/v1/provisioning/contact-points/"+uid, nil, nil, true)
	if status == http.StatusNotFound {
		return nil
	}
	return err
}

// ---- Alert rules -----------------------------------------------------------

// CreateAlertRule provisions an alert rule and returns its uid. Build the rule
// with BuildThresholdRule.
func (c *Client) CreateAlertRule(ctx context.Context, rule map[string]any) (string, error) {
	var out struct {
		UID string `json:"uid"`
	}
	if _, err := c.do(ctx, http.MethodPost, "/api/v1/provisioning/alert-rules", rule, &out, true); err != nil {
		return "", err
	}
	return out.UID, nil
}

// AlertRuleExists reports whether an alert rule with this uid is present in
// Grafana. Used by the reconcile loop to detect rules wiped by a Grafana
// restart (emptyDir-backed shared Grafana) so they can be re-provisioned.
func (c *Client) AlertRuleExists(ctx context.Context, uid string) (bool, error) {
	if uid == "" {
		return false, nil
	}
	status, err := c.do(ctx, http.MethodGet, "/api/v1/provisioning/alert-rules/"+uid, nil, nil, false)
	if err == nil {
		return true, nil
	}
	if status == http.StatusNotFound {
		return false, nil
	}
	return false, err
}

// ContactPointExists reports whether a contact point with this name exists. The
// reconcile loop uses it to decide whether an alert rule can still route to its
// channel — channel secrets (bot tokens, addresses) are not persisted backend
// side, so a wiped contact point cannot be re-created, only routed around.
func (c *Client) ContactPointExists(ctx context.Context, name string) (bool, error) {
	if name == "" {
		return false, nil
	}
	var out []ContactPoint
	if _, err := c.do(ctx, http.MethodGet,
		"/api/v1/provisioning/contact-points?name="+url.QueryEscape(name),
		nil, &out, false); err != nil {
		return false, err
	}
	return len(out) > 0, nil
}

// DeleteAlertRule removes an alert rule by uid. Missing is not an error.
func (c *Client) DeleteAlertRule(ctx context.Context, uid string) error {
	if uid == "" {
		return nil
	}
	status, err := c.do(ctx, http.MethodDelete, "/api/v1/provisioning/alert-rules/"+uid, nil, nil, true)
	if status == http.StatusNotFound {
		return nil
	}
	return err
}

// ---- Firing alerts (health input) ------------------------------------------

// alertmanagerAlert is one active alert from the Grafana Alertmanager view.
type alertmanagerAlert struct {
	Labels map[string]string `json:"labels"`
	Status struct {
		State string `json:"state"` // active | suppressed
	} `json:"status"`
}

// FiringAlerts returns the count of active alerts whose labels match every pair
// in `match` (e.g. {project_id, monitoring_app}). Used to raise health=critical.
func (c *Client) FiringAlerts(ctx context.Context, match map[string]string) (int, error) {
	var alerts []alertmanagerAlert
	if _, err := c.do(ctx, http.MethodGet,
		"/api/alertmanager/grafana/api/v2/alerts?active=true&silenced=false&inhibited=false",
		nil, &alerts, false); err != nil {
		return 0, err
	}
	n := 0
	for _, a := range alerts {
		if a.Status.State != "active" {
			continue
		}
		ok := true
		for k, v := range match {
			if a.Labels[k] != v {
				ok = false
				break
			}
		}
		if ok {
			n++
		}
	}
	return n, nil
}

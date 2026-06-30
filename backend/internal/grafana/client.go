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
	"crypto/rand"
	"encoding/hex"
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

// ---- Datasources (per project, Mimir tenant-scoped) ------------------------

// EnsureDatasource creates-or-updates a per-project Prometheus-type datasource
// that queries Mimir with a FIXED X-Scope-OrgID tenant header. Embedded
// dashboards and alert rules for the project point at this datasource, so they
// read ONLY this tenant's series — Mimir enforces the boundary regardless of the
// PromQL a user might craft, which a single shared datasource (relying solely on
// the project_id label) cannot guarantee. uid must be stable per project so the
// call is idempotent. tenant is the X-Scope-OrgID value (see
// monitoringReadTenant: project_id, optionally federated with the legacy tenant).
func (c *Client) EnsureDatasource(ctx context.Context, uid, name, mimirURL, tenant string) error {
	body := map[string]any{
		"uid":    uid,
		"name":   name,
		"type":   "prometheus",
		"access": "proxy",
		"url":    mimirURL,
		"jsonData": map[string]any{
			"httpMethod":      "POST",
			"httpHeaderName1": "X-Scope-OrgID",
		},
		"secureJsonData": map[string]any{
			"httpHeaderValue1": tenant,
		},
	}
	status, err := c.do(ctx, http.MethodGet, "/api/datasources/uid/"+uid, nil, nil, false)
	if err == nil {
		// PUT replaces the datasource (incl. the tenant header) for this uid.
		_, err = c.do(ctx, http.MethodPut, "/api/datasources/uid/"+uid, body, nil, false)
		return err
	}
	if status != http.StatusNotFound {
		return err
	}
	_, err = c.do(ctx, http.MethodPost, "/api/datasources", body, nil, false)
	// A concurrent create (same uid/name) returns 409 — treat as success.
	if err != nil && strings.Contains(err.Error(), "status 409") {
		return nil
	}
	return err
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

// Folder permission levels (Grafana folder/dashboard ACL ints).
const (
	permView  = 1
	permEdit  = 2
	permAdmin = 4
)

// folderPerm is one entry of a folder's ACL as returned by GET .../permissions.
// A grant is either role-based (Role set, e.g. "Viewer"), user-based (UserID),
// or team-based (TeamID).
type folderPerm struct {
	UserID     int    `json:"userId"`
	TeamID     int    `json:"teamId"`
	Role       string `json:"role"`
	Permission int    `json:"permission"`
}

// SetFolderTenant strips the inherited Editor/Viewer ROLE grants from a folder
// while preserving every explicit user/team grant, so the folder is reachable
// only by users explicitly granted via EnsureUserFolderAccess (plus org admins) —
// never by an arbitrary authenticated Viewer. This is the isolation baseline for
// embed auth on Grafana OSS (which lacks Enterprise Team Sync). Errors are
// non-fatal (older Grafana may reject); caller logs and continues.
func (c *Client) SetFolderTenant(ctx context.Context, folderUID string) error {
	return c.rebuildFolderPerms(ctx, folderUID, 0)
}

// EnsureUserFolderAccess makes the console user (identified by login) able to view
// a project folder under embed auth: it ensures a matching Grafana user exists
// (auth.proxy will authenticate the iframe request as this same login) and grants
// that user View on the folder, leaving other users' grants intact and dropping
// the broad role grants. Idempotent. This is what enforces cross-tenant isolation
// on Grafana OSS: only users the console has granted (i.e. members who opened the
// dashboard for a project they belong to) can render that project's folder.
func (c *Client) EnsureUserFolderAccess(ctx context.Context, folderUID, login, email, name string) error {
	uid, err := c.EnsureUser(ctx, login, email, name)
	if err != nil {
		return err
	}
	return c.rebuildFolderPerms(ctx, folderUID, uid)
}

// rebuildFolderPerms reads the current ACL, drops inherited role grants, keeps
// every explicit user/team grant, and (when addUserID > 0) ensures that user has
// View. The Grafana permissions API replaces the whole list on POST, so we must
// read-merge-write to avoid clobbering other users' access.
func (c *Client) rebuildFolderPerms(ctx context.Context, folderUID string, addUserID int) error {
	var cur []folderPerm
	if _, err := c.do(ctx, http.MethodGet, "/api/folders/"+folderUID+"/permissions", nil, &cur, false); err != nil {
		return err
	}
	items := make([]any, 0, len(cur)+1)
	haveUser := false
	for _, p := range cur {
		switch {
		case p.Role != "":
			// drop inherited Editor/Viewer role grants (the isolation strip)
		case p.UserID > 0:
			items = append(items, map[string]any{"userId": p.UserID, "permission": p.Permission})
			if p.UserID == addUserID {
				haveUser = true
			}
		case p.TeamID > 0:
			items = append(items, map[string]any{"teamId": p.TeamID, "permission": p.Permission})
		}
	}
	if addUserID > 0 && !haveUser {
		items = append(items, map[string]any{"userId": addUserID, "permission": permView})
	}
	body := map[string]any{"items": items}
	_, err := c.do(ctx, http.MethodPost, "/api/folders/"+folderUID+"/permissions", body, nil, false)
	return err
}

// ---- Users (per console user, for embed-auth folder isolation) -------------

// EnsureUser returns the Grafana user id for login, creating the user if absent
// (idempotent). Login must equal the X-WEBAUTH-USER the embed gateway asserts, so
// auth.proxy authenticates the iframe request as this same user. Created users get
// a random password they never use (auth is header-based).
func (c *Client) EnsureUser(ctx context.Context, login, email, name string) (int, error) {
	if id, err := c.lookupUser(ctx, login); err != nil {
		return 0, err
	} else if id > 0 {
		return id, nil
	}
	body := map[string]any{"login": login, "name": name, "password": randomPassword()}
	if email != "" {
		body["email"] = email
	}
	var out struct {
		ID int `json:"id"`
	}
	status, err := c.do(ctx, http.MethodPost, "/api/admin/users", body, &out, false)
	if err != nil {
		// Created concurrently / login already taken → re-look it up.
		if status == http.StatusConflict || status == http.StatusPreconditionFailed || status == http.StatusBadRequest {
			return c.lookupUser(ctx, login)
		}
		return 0, err
	}
	return out.ID, nil
}

// lookupUser returns the user id for a login/email, or 0 when none exists.
func (c *Client) lookupUser(ctx context.Context, login string) (int, error) {
	var out struct {
		ID int `json:"id"`
	}
	status, err := c.do(ctx, http.MethodGet, "/api/users/lookup?loginOrEmail="+url.QueryEscape(login), nil, &out, false)
	if err != nil {
		if status == http.StatusNotFound {
			return 0, nil
		}
		return 0, err
	}
	return out.ID, nil
}

// randomPassword returns a 32-hex-char password for an auth.proxy-managed user
// that never logs in with it. crypto/rand; falls back to a fixed-length filler
// only if the RNG fails (a created user with an unknown long password is still
// inaccessible by password since login is header-based).
func randomPassword() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "x0x0x0x0x0x0x0x0x0x0x0x0x0x0x0x0"
	}
	return hex.EncodeToString(b)
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

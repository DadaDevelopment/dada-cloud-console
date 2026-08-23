package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
)

// funnelWindows maps the UI's fixed window choices to a Postgres interval
// literal. "all" has no WHERE clause at all (interval math can't express
// "no lower bound").
var funnelWindows = map[string]string{
	"7d":  "7 days",
	"30d": "30 days",
	"90d": "90 days",
}

type adminFunnelCohortCount struct {
	AccountKind string `json:"account_kind"`
	Count       int    `json:"count"`
}

// adminFunnelChannel is one traffic source's top-of-funnel row. Visits is a
// visit count; every other number is a count of unique users, so the goal
// stages nest inside Users and inside each other.
type adminFunnelChannel struct {
	Source               string `json:"source"`
	Visits               int    `json:"visits"`
	Users                int    `json:"users"`
	RegisterOpened       int    `json:"register_opened"`
	SignupStarted        int    `json:"signup_started"`
	RegistrationComplete int    `json:"registration_complete"`
	DeploySuccess        int    `json:"deploy_success"`
}

type adminFunnelChannelReport struct {
	Available bool                 `json:"available"`
	Days      int                  `json:"days"`
	Channels  []adminFunnelChannel `json:"channels"`
	Totals    adminFunnelChannel   `json:"totals"`
	Note      string               `json:"note,omitempty"`
}

type adminFunnelAcquisition struct {
	UXLandingUsers       int `json:"ux_landing_users"`
	UXSignupStartedUsers int `json:"ux_signup_started_users"`
	AccountsCreated      int `json:"accounts_created"`
	FirstAuthenticated   int `json:"first_authenticated"`
}

type adminFunnelResource struct {
	Key            string `json:"key"`
	RequestedUsers int    `json:"requested_users"`
	Requests       int    `json:"requests"`
	ReadyUsers     int    `json:"ready_users"`
}

type adminFunnelLifecycle struct {
	CustomerAccounts        int                   `json:"customer_accounts"`
	ProjectOwners           int                   `json:"project_owners"`
	ResourceRequesters      int                   `json:"resource_requesters"`
	ReadyResourceUsers      int                   `json:"ready_resource_users"`
	ResourceOrganizations   int                   `json:"resource_organizations"`
	CheckoutOrganizations   int                   `json:"checkout_organizations"`
	PaidOrganizations       int                   `json:"paid_organizations"`
	QuotaBlockedUsers       int                   `json:"quota_blocked_users"`
	QuotaBlockedAttempts    int                   `json:"quota_blocked_attempts"`
	QuotaGraceOrganizations int                   `json:"quota_grace_organizations"`
	AppCreators             int                   `json:"app_creators"`
	GitConnectedUsers       int                   `json:"git_connected_users"`
	BuildStartedUsers       int                   `json:"build_started_users"`
	FirstDeployedUsers      int                   `json:"first_deployed_users"`
	Resources               []adminFunnelResource `json:"resources"`
}

type adminFunnelResponse struct {
	Window        string                   `json:"window"`
	ExcludedKinds []string                 `json:"excluded_kinds"`
	CohortCounts  []adminFunnelCohortCount `json:"cohort_counts"`
	ChannelFunnel adminFunnelChannelReport `json:"channel_funnel"`
	Acquisition   adminFunnelAcquisition   `json:"acquisition"`
	Lifecycle     adminFunnelLifecycle     `json:"lifecycle"`
}

const cloudFunnelCounterID = 110158915

const (
	cloudGoalRegisterOpened       = 585010094
	cloudGoalSignupStarted        = 593177849
	cloudGoalRegistrationComplete = 586052031
	cloudGoalDeploySuccess        = 585205874
)

var metrikaStatAPIURL = "https://api-metrika.yandex.net/stat/v1/data"

func adminFunnelAcquisitionQuery(sinceClause, uxSinceClause string) string {
	return `
		WITH account_scope AS (
			SELECT u.id, u.created_at
			FROM user_accounts u
			WHERE u.account_kind = 'customer'
			  AND ` + sinceClause + `
			  AND ($1::text[] IS NULL OR u.account_kind <> ALL($1))
		),
		first_authenticated AS (
			SELECT DISTINCT s.id
			FROM account_scope s
			JOIN audit_events a ON a.actor_id = s.id
				AND a.action = 'SessionStart'
				AND a.metadata->>'visit' = 'first'
				AND a.created_at >= s.created_at
		),
		resolved_ux AS (
			SELECT COALESCE(x.user_id::text, i.user_id::text, x.anon_id::text) AS identity, x.event_type, x.target
			FROM ux_events x
			LEFT JOIN ux_identity i ON i.anon_id = x.anon_id
			WHERE ` + uxSinceClause + `
		)
		SELECT
			(SELECT count(DISTINCT identity) FROM resolved_ux WHERE event_type = 'pageview' AND target IN ('/', '/en')),
			(SELECT count(DISTINCT identity) FROM resolved_ux WHERE event_type = 'goal' AND target = 'signup_started'),
			(SELECT count(*) FROM account_scope),
			(SELECT count(*) FROM first_authenticated)
	`
}

func adminFunnelLifecycleQuery() string {
	return `
		WITH scope AS (
			SELECT u.id
			FROM user_accounts u
			WHERE u.account_kind = 'customer'
			  AND ($1::text[] IS NULL OR u.account_kind <> ALL($1))
		),
		proj AS (
			SELECT p.id, p.owner_id, p.org_id
			FROM projects p
			JOIN scope s ON s.id = p.owner_id
		),
		requested AS (
			SELECT DISTINCT p.owner_id, p.org_id
			FROM operations o
			JOIN proj p ON p.id = o.project_id
			WHERE o.action IN ('CreateApp', 'CreateServiceDatabase', 'CreateS3Bucket', 'CreateAppServer', 'BoxUp')
			  AND o.status NOT IN ('Failed', 'Cancelled')
		),
		ready AS (
			SELECT DISTINCT p.owner_id FROM proj p JOIN resource_snapshots rs ON rs.project_id = p.id
				WHERE rs.phase = 'Ready' AND rs.kind IN ('App', 'ServiceDatabase', 'ServiceDatabaseV2', 'S3Bucket')
			UNION
			SELECT DISTINCT p.owner_id FROM proj p JOIN app_servers a ON a.project_id = p.id WHERE a.status = 'Ready'
			UNION
			SELECT DISTINCT p.owner_id FROM proj p JOIN boxes b ON b.project_id = p.id WHERE b.status IN ('Ready', 'Idle')
		),
		resource_orgs AS (
			SELECT DISTINCT org_id FROM requested
		),
		checkout_orgs AS (
			SELECT DISTINCT org_id FROM payments WHERE status IN ('pending', 'succeeded', 'canceled')
		),
		paid_orgs AS (
			SELECT DISTINCT org_id FROM payments WHERE status = 'succeeded' AND paid_at IS NOT NULL
		),
		quota_blocked AS (
			SELECT a.actor_id, count(*) AS attempts
			FROM audit_events a
			JOIN scope s ON s.id = a.actor_id
			WHERE a.outcome = 'failure'
			  AND a.metadata->>'reason' IN ('quota_exceeded', 'consumption_exceeded', 'storage_quota_exceeded')
			GROUP BY a.actor_id
		),
		grace_orgs AS (
			SELECT DISTINCT b.org_id FROM billing_accounts b JOIN resource_orgs r ON r.org_id = b.org_id
			WHERE b.quota_breach_count > 0
		)
		SELECT
			(SELECT count(*) FROM scope),
			(SELECT count(DISTINCT owner_id) FROM proj),
			(SELECT count(DISTINCT owner_id) FROM requested),
			(SELECT count(*) FROM ready),
			(SELECT count(*) FROM resource_orgs),
			(SELECT count(*) FROM resource_orgs r JOIN checkout_orgs c USING (org_id)),
			(SELECT count(*) FROM resource_orgs r JOIN paid_orgs p USING (org_id)),
			(SELECT count(*) FROM quota_blocked),
			(SELECT COALESCE(sum(attempts), 0) FROM quota_blocked),
			(SELECT count(*) FROM grace_orgs)
	`
}

func adminFunnelResourcesQuery() string {
	return `
		WITH scope AS (
			SELECT u.id FROM user_accounts u WHERE u.account_kind = 'customer' AND ($1::text[] IS NULL OR u.account_kind <> ALL($1))
		),
		proj AS (
			SELECT p.id, p.owner_id FROM projects p JOIN scope s ON s.id = p.owner_id
		),
		kinds AS (
			SELECT 'app'::text AS key, 'CreateApp'::text AS action
			UNION ALL SELECT 'db', 'CreateServiceDatabase'
			UNION ALL SELECT 'vm', 'CreateAppServer'
			UNION ALL SELECT 'box', 'BoxUp'
			UNION ALL SELECT 's3', 'CreateS3Bucket'
		),
		request_counts AS (
			SELECT k.key, count(DISTINCT p.owner_id) AS users, count(*) AS requests
			FROM kinds k JOIN operations o ON o.action = k.action
			JOIN proj p ON p.id = o.project_id
			WHERE o.status NOT IN ('Failed', 'Cancelled')
			GROUP BY k.key
		),
		ready AS (
			SELECT 'app'::text AS key, rs.project_id FROM resource_snapshots rs WHERE rs.kind = 'App' AND rs.phase = 'Ready'
			UNION ALL SELECT 'db', rs.project_id FROM resource_snapshots rs WHERE rs.kind IN ('ServiceDatabase', 'ServiceDatabaseV2') AND rs.phase = 'Ready'
			UNION ALL SELECT 'vm', a.project_id FROM app_servers a WHERE a.status = 'Ready'
			UNION ALL SELECT 'box', b.project_id FROM boxes b WHERE b.status IN ('Ready', 'Idle')
			UNION ALL SELECT 's3', rs.project_id FROM resource_snapshots rs WHERE rs.kind = 'S3Bucket' AND rs.phase = 'Ready'
		),
		ready_counts AS (
			SELECT rd.key, count(DISTINCT p.owner_id) AS users
			FROM ready rd JOIN proj p ON p.id = rd.project_id
			GROUP BY rd.key
		)
		SELECT
			k.key,
			COALESCE(r.users, 0),
			COALESCE(r.requests, 0),
			COALESCE(rd.users, 0)
		FROM kinds k
		LEFT JOIN request_counts r ON r.key = k.key
		LEFT JOIN ready_counts rd ON rd.key = k.key
		ORDER BY k.key
	`
}

func adminFunnelFirstDeployQuery() string {
	return `
		WITH scope AS (
			SELECT u.id FROM user_accounts u
			WHERE u.account_kind = 'customer'
			  AND ($1::text[] IS NULL OR u.account_kind <> ALL($1))
		),
		apps AS (
			SELECT a.actor_id, a.project_id, a.environment_id, a.resource_name AS app_name, a.created_at AS app_created_at
			FROM audit_events a
			JOIN scope s ON s.id = a.actor_id
			WHERE a.action = 'CreateApp' AND a.outcome = 'success' AND a.resource_kind = 'App'
		),
		repos AS (
			SELECT DISTINCT ON (a.actor_id, a.project_id, a.environment_id, a.app_name)
				a.actor_id, a.project_id, a.environment_id, a.app_name, g.created_at AS connected_at
			FROM apps a
			JOIN audit_events g ON g.actor_id = a.actor_id
				AND g.project_id = a.project_id
				AND g.environment_id = a.environment_id
				AND g.resource_name = a.app_name
				AND g.action = 'ConnectGitRepo'
				AND g.outcome = 'success'
				AND g.created_at >= a.app_created_at
			ORDER BY a.actor_id, a.project_id, a.environment_id, a.app_name, g.created_at
		),
		started_builds AS (
			SELECT DISTINCT ON (r.actor_id, r.project_id, r.environment_id, r.app_name)
				r.actor_id, r.project_id, r.environment_id, r.app_name, b.id AS build_id, b.started_at
			FROM repos r
			JOIN git_repos gr ON gr.project_id = r.project_id AND gr.environment_id = r.environment_id AND gr.app_name = r.app_name
			JOIN builds b ON b.git_repo_id = gr.id AND b.environment_id = r.environment_id AND b.app_name = r.app_name
			WHERE b.started_at IS NOT NULL AND b.started_at >= r.connected_at
			ORDER BY r.actor_id, r.project_id, r.environment_id, r.app_name, b.started_at
		),
		deployed AS (
			SELECT DISTINCT b.actor_id
			FROM started_builds b
			JOIN deployments d ON d.build_id = b.build_id AND d.created_at >= b.started_at
			LEFT JOIN operations o ON o.id = d.operation_id
			WHERE d.operation_id IS NULL OR (o.action = 'DeployImageVersion' AND o.status IN ('Committed', 'Ready'))
		)
		SELECT
			(SELECT count(DISTINCT actor_id) FROM apps),
			(SELECT count(DISTINCT actor_id) FROM repos),
			(SELECT count(DISTINCT actor_id) FROM started_builds),
			(SELECT count(*) FROM deployed)
	`
}

func adminFunnelCohortsQuery(sinceClause string) string {
	return `SELECT u.account_kind, count(*) FROM user_accounts u WHERE ` + sinceClause + ` GROUP BY u.account_kind ORDER BY u.account_kind`
}

// funnelWindowDays maps the UI's fixed window choices to a day count for the
// Keycloak/Metrika registration leg, which speaks days rather than a Postgres
// interval literal. "all" has no true bound; 3650 (10y) is a stand-in that
// comfortably covers the product's entire lifetime.
var funnelWindowDays = map[string]int{
	"7d": 7, "30d": 30, "90d": 90, "all": 3650,
}

// GetAdminFunnel returns two countable product paths. Acquisition keeps the
// anonymous Metrika and browser-identity stages visibly separate from account
// rows and first authenticated entries. Lifecycle follows customer accounts to
// projects and resource requests, then switches deliberately to organizations
// for checkout and payment because billing belongs to the organization.
//
// Resource readiness is a current-state snapshot: App/DB/S3 use the reconciler
// phase, VM uses its status, and Box uses Ready or Idle. It is not presented as
// a historical first-ready event because that timestamp is not stored for each
// resource kind.
//
// @ID          getAdminFunnel
// @Summary     Full acquisition and product funnel
// @Description Detailed acquisition and lifecycle funnels. The window applies to acquisition; lifecycle is historical for all customer accounts.
// @Tags        admin
// @Param       window query string false "7d|30d|90d|all" default(30d)
// @Param       exclude_kind query string false "comma-separated account_kind values to hide"
// @Success     200 {object} adminFunnelResponse
// @Router      /admin/funnel [get]
func (h *Handler) GetAdminFunnel(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isGod(claims) && !isPlatformAnalyst(claims) {
		respondForbidden(c)
		return
	}

	window := c.DefaultQuery("window", "30d")
	interval, known := funnelWindows[window]
	if !known && window != "all" {
		respondError(c, http.StatusBadRequest, "invalid window, expected 7d|30d|90d|all")
		return
	}

	var excludeKinds []string
	if raw := c.Query("exclude_kind"); raw != "" {
		for _, k := range strings.Split(raw, ",") {
			k = strings.TrimSpace(k)
			if k != "" {
				excludeKinds = append(excludeKinds, k)
			}
		}
	}

	sinceClause := "TRUE"
	uxSinceClause := "TRUE"
	if interval != "" {
		sinceClause = "u.created_at >= now() - interval '" + interval + "'"
		uxSinceClause = "x.occurred_at >= now() - interval '" + interval + "'"
	}

	var excludeArg interface{}
	if len(excludeKinds) > 0 {
		excludeArg = excludeKinds
	}

	resp := adminFunnelResponse{Window: window, ExcludedKinds: excludeKinds}
	err := h.pool.QueryRow(c.Request.Context(), adminFunnelAcquisitionQuery(sinceClause, uxSinceClause), excludeArg).Scan(
		&resp.Acquisition.UXLandingUsers, &resp.Acquisition.UXSignupStartedUsers,
		&resp.Acquisition.AccountsCreated, &resp.Acquisition.FirstAuthenticated,
	)
	if err != nil {
		log.Printf("admin funnel: read acquisition: %v", err)
		respondError(c, http.StatusInternalServerError, "failed to read funnel")
		return
	}
	if err := h.pool.QueryRow(c.Request.Context(), adminFunnelLifecycleQuery(), excludeArg).Scan(
		&resp.Lifecycle.CustomerAccounts, &resp.Lifecycle.ProjectOwners, &resp.Lifecycle.ResourceRequesters,
		&resp.Lifecycle.ReadyResourceUsers, &resp.Lifecycle.ResourceOrganizations,
		&resp.Lifecycle.CheckoutOrganizations, &resp.Lifecycle.PaidOrganizations,
		&resp.Lifecycle.QuotaBlockedUsers, &resp.Lifecycle.QuotaBlockedAttempts,
		&resp.Lifecycle.QuotaGraceOrganizations,
	); err != nil {
		log.Printf("admin funnel: read lifecycle: %v", err)
		respondError(c, http.StatusInternalServerError, "failed to read funnel")
		return
	}
	if err := h.pool.QueryRow(c.Request.Context(), adminFunnelFirstDeployQuery(), excludeArg).Scan(
		&resp.Lifecycle.AppCreators, &resp.Lifecycle.GitConnectedUsers,
		&resp.Lifecycle.BuildStartedUsers, &resp.Lifecycle.FirstDeployedUsers,
	); err != nil {
		log.Printf("admin funnel: read first deploy path: %v", err)
		respondError(c, http.StatusInternalServerError, "failed to read funnel")
		return
	}

	resourceRows, err := h.pool.Query(c.Request.Context(), adminFunnelResourcesQuery(), excludeArg)
	if err != nil {
		log.Printf("admin funnel: read resource mix: %v", err)
		respondError(c, http.StatusInternalServerError, "failed to read funnel")
		return
	}
	defer resourceRows.Close()
	resp.Lifecycle.Resources = make([]adminFunnelResource, 0, 5)
	for resourceRows.Next() {
		var resource adminFunnelResource
		if err := resourceRows.Scan(&resource.Key, &resource.RequestedUsers, &resource.Requests, &resource.ReadyUsers); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan funnel resources")
			return
		}
		resp.Lifecycle.Resources = append(resp.Lifecycle.Resources, resource)
	}
	if err := resourceRows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read funnel resources")
		return
	}

	cohortRows, err := h.pool.Query(c.Request.Context(), adminFunnelCohortsQuery(sinceClause))
	if err != nil {
		log.Printf("admin funnel: read cohorts: %v", err)
		respondError(c, http.StatusInternalServerError, "failed to read funnel cohorts")
		return
	}
	defer cohortRows.Close()
	resp.CohortCounts = make([]adminFunnelCohortCount, 0)
	for cohortRows.Next() {
		var cc adminFunnelCohortCount
		if err := cohortRows.Scan(&cc.AccountKind, &cc.Count); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan funnel cohorts")
			return
		}
		resp.CohortCounts = append(resp.CohortCounts, cc)
	}

	resp.ChannelFunnel = h.adminFunnelChannelReport(c.Request.Context(), funnelWindowDays[window])

	c.JSON(http.StatusOK, resp)
}

func (h *Handler) adminFunnelChannelReport(ctx context.Context, days int) adminFunnelChannelReport {
	out := adminFunnelChannelReport{Days: days}
	if h.cfg.MetrikaOAuthToken == "" {
		out.Note = "METRIKA_OAUTH_TOKEN не настроен"
		return out
	}

	channels, totals, err := fetchMetrikaTrafficSourceFunnel(ctx, h.cfg.MetrikaOAuthToken, days)
	if err != nil {
		log.Printf("admin funnel: read Metrika channels: %v", err)
		out.Note = "Yandex Metrika недоступна: " + err.Error()
		return out
	}

	out.Available = true
	out.Channels = channels
	out.Totals = totals
	return out
}

type metrikaTrafficSourceRow struct {
	Dimensions []struct {
		Name string `json:"name"`
	} `json:"dimensions"`
	Metrics []float64 `json:"metrics"`
}

type metrikaTrafficSourceReport struct {
	Data   []metrikaTrafficSourceRow `json:"data"`
	Totals []float64                 `json:"totals"`
}

// fetchMetrikaTrafficSourceFunnel reads the per-traffic-source top of the
// funnel from Metrika.
//
// Goal stages use ym:s:goal<id>users (unique users who reached the goal), NOT
// ym:s:goal<id>reaches (raw event count). A funnel stage has to be countable
// against the stage above it, and reaches is not: one person deploying nine
// times contributes nine reaches, which produced the production reading where
// "successful deploy" (22) exceeded "registration complete" (5) on the same
// source and made every downstream ribbon read as growth.
func fetchMetrikaTrafficSourceFunnel(ctx context.Context, oauthToken string, days int) ([]adminFunnelChannel, adminFunnelChannel, error) {
	metrics := []string{
		"ym:s:visits",
		"ym:s:users",
		fmt.Sprintf("ym:s:goal%dusers", cloudGoalRegisterOpened),
		fmt.Sprintf("ym:s:goal%dusers", cloudGoalSignupStarted),
		fmt.Sprintf("ym:s:goal%dusers", cloudGoalRegistrationComplete),
		fmt.Sprintf("ym:s:goal%dusers", cloudGoalDeploySuccess),
	}
	q := url.Values{}
	q.Set("ids", strconv.Itoa(cloudFunnelCounterID))
	q.Set("metrics", strings.Join(metrics, ","))
	q.Set("dimensions", "ym:s:trafficSource")
	q.Set("date1", strconv.Itoa(days)+"daysAgo")
	q.Set("date2", "today")
	q.Set("accuracy", "full")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metrikaStatAPIURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, adminFunnelChannel{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "OAuth "+oauthToken)
	req.Header.Set("Accept", "application/json")

	resp, err := metrikaStatHTTPClient.Do(req)
	if err != nil {
		return nil, adminFunnelChannel{}, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, adminFunnelChannel{}, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed metrikaTrafficSourceReport
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, adminFunnelChannel{}, fmt.Errorf("decode: %w", err)
	}

	channels := make([]adminFunnelChannel, 0, len(parsed.Data))
	for _, row := range parsed.Data {
		if len(row.Dimensions) == 0 || len(row.Metrics) != len(metrics) {
			return nil, adminFunnelChannel{}, fmt.Errorf("unexpected channel report row")
		}
		channels = append(channels, adminFunnelChannelFromMetrics(row.Dimensions[0].Name, row.Metrics))
	}
	if len(parsed.Totals) != len(metrics) {
		return nil, adminFunnelChannel{}, fmt.Errorf("unexpected channel report totals")
	}
	return channels, adminFunnelChannelFromMetrics("Все источники", parsed.Totals), nil
}

func adminFunnelChannelFromMetrics(source string, metrics []float64) adminFunnelChannel {
	return adminFunnelChannel{
		Source:               source,
		Visits:               int(metrics[0]),
		Users:                int(metrics[1]),
		RegisterOpened:       int(metrics[2]),
		SignupStarted:        int(metrics[3]),
		RegistrationComplete: int(metrics[4]),
		DeploySuccess:        int(metrics[5]),
	}
}

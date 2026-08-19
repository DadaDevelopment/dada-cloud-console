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

type adminFunnelResponse struct {
	Window             string                     `json:"window"`
	ExcludedKinds      []string                   `json:"excluded_kinds"`
	Signups            int                        `json:"signups"`
	AppUp              int                        `json:"app_up"`
	DBUp               int                        `json:"db_up"`
	VMUp               int                        `json:"vm_up"`
	BoxUp              int                        `json:"box_up"`
	S3Up               int                        `json:"s3_up"`
	ModelUp            int                        `json:"model_up"`
	Paid               int                        `json:"paid"`
	PaidNote           string                     `json:"paid_note,omitempty"`
	CohortCounts       []adminFunnelCohortCount   `json:"cohort_counts"`
	ChannelFunnel      adminFunnelChannelReport   `json:"channel_funnel"`
	RegistrationFunnel overviewRegistrationFunnel `json:"registration_funnel"`
}

const cloudFunnelCounterID = 110158915

const (
	cloudGoalRegisterOpened       = 585010094
	cloudGoalSignupStarted        = 593177849
	cloudGoalRegistrationComplete = 586052031
	cloudGoalDeploySuccess        = 585205874
)

var metrikaStatAPIURL = "https://api-metrika.yandex.net/stat/v1/data"

func adminFunnelCountsQuery(sinceClause string) string {
	return `
		WITH scope AS (
			SELECT u.id, u.email
			FROM user_accounts u
			WHERE ` + sinceClause + `
			  AND ($1::text[] IS NULL OR u.account_kind <> ALL($1))
		),
		proj AS (
			SELECT p.id, p.owner_id FROM projects p JOIN scope s ON s.id = p.owner_id
		)
		SELECT
			(SELECT count(*) FROM scope),
			(SELECT count(DISTINCT p.owner_id) FROM resource_snapshots rs JOIN proj p ON p.id = rs.project_id
				WHERE rs.kind = 'App' AND rs.phase = 'Ready'),
			(SELECT count(DISTINCT p.owner_id) FROM resource_snapshots rs JOIN proj p ON p.id = rs.project_id
				WHERE rs.kind IN ('ServiceDatabase','ServiceDatabaseV2') AND rs.phase = 'Ready'),
			(SELECT count(DISTINCT p.owner_id) FROM app_servers a JOIN proj p ON p.id = a.project_id
				WHERE a.status = 'Ready' OR a.vm_ip <> ''),
			(SELECT count(DISTINCT p.owner_id) FROM boxes b JOIN proj p ON p.id = b.project_id
				WHERE b.ssh_host <> ''),
			(SELECT count(DISTINCT p.owner_id) FROM resource_snapshots rs JOIN proj p ON p.id = rs.project_id
				WHERE rs.kind = 'S3Bucket' AND rs.phase = 'Ready'),
			(SELECT count(DISTINCT p.owner_id) FROM resource_snapshots rs JOIN proj p ON p.id = rs.project_id
				WHERE rs.kind = 'AIModel'),
			(SELECT count(DISTINCT s.id) FROM scope s JOIN payments pay ON lower(pay.customer_email) = lower(s.email)
				WHERE pay.status = 'succeeded')
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

// GetAdminFunnel returns the live Metrika channel leg, Keycloak registration
// leg, and product-adoption counts (signup -> App/DB/VM/Box/S3/Model -> paid)
// for one window and account_kind cohort.
//
// Resource "up" is computed per resource kind, not per row, because the
// ground-truth signal differs by kind: App/DB/S3 use resource_snapshots.phase
// = 'Ready' (the gitops reconciler's verdict); AIModel has no working phase
// signal (the worker never stamps Ready for it), so presence is the only
// available proxy; AppServer (VM) and Box are not in resource_snapshots at
// all and use their own tables' "ever became reachable" columns (vm_ip / ssh_host)
// instead of current status, because status cycles back through
// Deleted/Failed after use and a status filter reads as a false zero.
//
// Paid joins payments to users via customer_email, not created_by_sub:
// created_by_sub is empty on every existing production row (P0, undiagnosed
// prior to this endpoint), while customer_email is a required, always-populated
// field at checkout (yookassa.Checkout enforces it via requireReceiptEmail).
//
// @ID          getAdminFunnel
// @Summary     Full acquisition and product funnel
// @Description Metrika traffic sources, Keycloak registration steps, and resource-kind adoption -> paid counts for a window. account_kind filters apply to the DB-backed product leg only.
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
	if interval != "" {
		sinceClause = "u.created_at >= now() - interval '" + interval + "'"
	}

	var excludeArg interface{}
	if len(excludeKinds) > 0 {
		excludeArg = excludeKinds
	}

	resp := adminFunnelResponse{Window: window, ExcludedKinds: excludeKinds}
	err := h.pool.QueryRow(c.Request.Context(), adminFunnelCountsQuery(sinceClause), excludeArg).Scan(
		&resp.Signups, &resp.AppUp, &resp.DBUp, &resp.VMUp, &resp.BoxUp, &resp.S3Up, &resp.ModelUp, &resp.Paid,
	)
	if err != nil {
		log.Printf("admin funnel: read counts: %v", err)
		respondError(c, http.StatusInternalServerError, "failed to read funnel")
		return
	}
	resp.PaidNote = "AIModel counts row presence only, its phase never reaches Ready; VM/Box count ever-reachable, not current status."

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

	resp.RegistrationFunnel = h.overviewRegistrationFunnel(c.Request.Context(), funnelWindowDays[window])
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

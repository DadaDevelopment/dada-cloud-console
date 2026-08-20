package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/rs/zerolog/log"
)

// pulseGitHubBaseURL is a package variable, not a constant, purely so tests
// can point it at an httptest.Server instead of api.github.com. Production
// code never mutates it.
var pulseGitHubBaseURL = "https://api.github.com"

// PulseExporter ships an hourly platform-state snapshot to a branch of
// argo-infra over the GitHub Contents API. It exists because the machine
// running the owner's hourly routine sometimes has no TCP route to the RU
// cluster at all -- every RU host times out -- while GitHub stays reachable,
// and gitops-agent's own commits already prove that path works every cycle
// (backend/internal/git/manager.go pushes to the same repo on every sync).
// The snapshot rides the same aggregation the admin overview panel serves,
// via Handler.BuildAdminOverview, so the two can never quietly disagree.
// buildOverview and collectCounters are indirected through struct fields
// rather than called directly, purely so pulse_export_test.go can substitute
// a DB-free and network-free stand-in while still exercising the real
// marshal/publish path.
type PulseExporter struct {
	h          *Handler
	cfg        *config.Config
	httpClient *http.Client

	buildOverview   func(ctx context.Context) (map[string]any, error)
	collectCounters func(ctx context.Context) (pulseCounters, []string)
}

// NewPulseExporter wires a PulseExporter to the same Handler the HTTP routes
// use, so BuildAdminOverview runs the identical queries the live panel does.
func NewPulseExporter(h *Handler, cfg *config.Config) *PulseExporter {
	p := &PulseExporter{
		h:   h,
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
	p.buildOverview = func(ctx context.Context) (map[string]any, error) {
		return p.h.BuildAdminOverview(ctx, overviewDynamicsDefaultDays)
	}
	p.collectCounters = p.collectPulseCountersFromDB
	return p
}

// Enabled reports whether the export has what it needs to run: a token
// (PULSE_EXPORT_TOKEN, falling back to GITOPS_DEFAULT_TOKEN in config.Load)
// and a target repo.
func (p *PulseExporter) Enabled() bool {
	return p.cfg.PulseExportToken != "" && p.cfg.PulseExportRepo != ""
}

// pulseCounters are point-in-time deltas that overview's day-granularity
// dynamics series cannot show: registration and build activity inside the
// current hour, feedback backlog growth, and stuck-payment count. Every
// field is filled independently in collectPulseCounters so one query's
// failure does not blank the others.
type pulseCounters struct {
	NewUsers1h           *int           `json:"new_users_1h,omitempty"`
	NewUsers24h          *int           `json:"new_users_24h,omitempty"`
	BuildsLastHour       map[string]int `json:"builds_last_hour,omitempty"`
	FeedbackNew24h       *int           `json:"feedback_new_24h,omitempty"`
	PaymentsPendingStale *int           `json:"payments_pending_stale,omitempty"`
}

// collectPulseCountersFromDB gathers the hourly deltas directly against the
// pool rather than through BuildAdminOverview, since none of these live in
// the overview payload today. A failing sub-query is appended to errs and
// its counter field left nil (omitted from the JSON) instead of aborting
// the whole tick -- the same "partial beats silently blank" contract
// BuildAdminOverview already follows for money.
func (p *PulseExporter) collectPulseCountersFromDB(ctx context.Context) (pulseCounters, []string) {
	var out pulseCounters
	var errs []string

	var new1h, new24h int
	err := p.h.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE created_at >= now() - interval '1 hour'),
			count(*) FILTER (WHERE created_at >= now() - interval '24 hours')
		FROM user_accounts
		WHERE account_kind = $1`,
		overviewCustomerKind,
	).Scan(&new1h, &new24h)
	if err != nil {
		errs = append(errs, fmt.Sprintf("new_users query failed: %v", err))
	} else {
		out.NewUsers1h = &new1h
		out.NewUsers24h = &new24h
	}

	rows, err := p.h.pool.Query(ctx, `
		SELECT status, count(*)
		FROM builds
		WHERE created_at >= now() - interval '1 hour'
		GROUP BY status`)
	if err != nil {
		errs = append(errs, fmt.Sprintf("builds_last_hour query failed: %v", err))
	} else {
		byStatus := map[string]int{}
		for rows.Next() {
			var status string
			var n int
			if scanErr := rows.Scan(&status, &n); scanErr != nil {
				errs = append(errs, fmt.Sprintf("builds_last_hour scan failed: %v", scanErr))
				break
			}
			byStatus[status] = n
		}
		rows.Close()
		if rows.Err() == nil {
			out.BuildsLastHour = byStatus
		}
	}

	var feedback24h int
	err = p.h.pool.QueryRow(ctx, `
		SELECT count(*) FROM feedback WHERE created_at >= now() - interval '24 hours'`,
	).Scan(&feedback24h)
	if err != nil {
		errs = append(errs, fmt.Sprintf("feedback_new_24h query failed: %v", err))
	} else {
		out.FeedbackNew24h = &feedback24h
	}

	var pendingStale int
	err = p.h.pool.QueryRow(ctx, `
		SELECT count(*) FROM payments
		WHERE status = 'pending' AND created_at < now() - interval '1 hour'`,
	).Scan(&pendingStale)
	if err != nil {
		errs = append(errs, fmt.Sprintf("payments_pending_stale query failed: %v", err))
	} else {
		out.PaymentsPendingStale = &pendingStale
	}

	return out, errs
}

// buildPulseSnapshot assembles the JSON payload one tick publishes. The
// "no overview key on total failure" rule below is the load-bearing part:
// the admin panel's own "blindness read as health" postmortem
// (project_admin_broken_panel_read_health_from_own_blindness) happened
// because an empty/absent signal was indistinguishable from a healthy one.
// A pulse snapshot with a populated "overview" but zeroed-out fields would
// repeat that mistake one hop further from the cluster, so a total collection
// failure omits "overview" entirely and leaves only "errors" for the reader
// to see the outage, never a payload shaped like a healthy one.
func (p *PulseExporter) buildPulseSnapshot(ctx context.Context) map[string]any {
	snapshot := map[string]any{
		"generated_at": time.Now().UTC().Format(time.RFC3339),
	}
	var errs []string

	overview, err := p.buildOverview(ctx)
	if err != nil {
		errs = append(errs, fmt.Sprintf("overview collection failed: %v", err))
	} else {
		snapshot["overview"] = overview
	}

	counters, counterErrs := p.collectCounters(ctx)
	errs = append(errs, counterErrs...)
	snapshot["counters"] = counters

	snapshot["errors"] = errs
	return snapshot
}

// RunPulseExportTick collects one snapshot and publishes it to GitHub. It
// never returns an error to the caller -- the caller is a ticker loop, and a
// GitHub outage or a DB hiccup must not kill the goroutine, only get logged
// so the next hourly tick can try again.
func (p *PulseExporter) RunPulseExportTick(ctx context.Context) {
	snapshot := p.buildPulseSnapshot(ctx)
	body, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		log.Warn().Err(err).Msg("pulse export: failed to marshal snapshot")
		return
	}

	if err := p.publishFile(ctx, "pulse/latest.json", body); err != nil {
		log.Warn().Err(err).Msg("pulse export: failed to publish pulse/latest.json")
		return
	}
	log.Info().Int("bytes", len(body)).Str("path", "pulse/latest.json").Msg("pulse export: published")

	historyPath := fmt.Sprintf("pulse/history/%s.json", time.Now().UTC().Format("2006-01-02T15"))
	if err := p.publishFile(ctx, historyPath, body); err != nil {
		log.Warn().Err(err).Str("path", historyPath).Msg("pulse export: failed to publish history snapshot")
		return
	}
	log.Info().Int("bytes", len(body)).Str("path", historyPath).Msg("pulse export: published")
}

// githubContentsGetResponse is the subset of GitHub's "Get repository
// content" response this exporter needs: just the blob SHA, to send back on
// the update PUT so GitHub accepts it as an edit instead of rejecting it as
// a conflicting create.
type githubContentsGetResponse struct {
	SHA string `json:"sha"`
}

// githubContentsPutRequest mirrors GitHub's "Create or update file contents"
// request body. SHA is omitted on first publish (the file does not exist
// yet, and GitHub 404s the SHA lookup) and set on every update after.
type githubContentsPutRequest struct {
	Message string `json:"message"`
	Content string `json:"content"`
	Branch  string `json:"branch"`
	SHA     string `json:"sha,omitempty"`
}

// publishFile PUTs one file's content to the configured repo/branch, first
// looking up the current blob SHA (a 404 there just means the file does not
// exist yet, which is the normal first-publish case, not an error).
func (p *PulseExporter) publishFile(ctx context.Context, path string, content []byte) error {
	sha, err := p.currentFileSHA(ctx, path)
	if err != nil {
		return err
	}

	reqBody := githubContentsPutRequest{
		Message: fmt.Sprintf("pulse: %s snapshot", path),
		Content: base64.StdEncoding.EncodeToString(content),
		Branch:  p.cfg.PulseExportBranch,
		SHA:     sha,
	}
	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/contents/%s", pulseGitHubBaseURL, p.cfg.PulseExportRepo, path)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(reqJSON))
	if err != nil {
		return fmt.Errorf("build PUT request: %w", err)
	}
	p.setGitHubHeaders(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("PUT %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("PUT %s: unexpected status %d: %s", path, resp.StatusCode, string(respBody))
	}
	return nil
}

// currentFileSHA looks up the existing blob SHA for path on the configured
// branch. A 404 is the normal "file does not exist yet" case and returns an
// empty SHA with no error; any other non-200 status is a real failure.
func (p *PulseExporter) currentFileSHA(ctx context.Context, path string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/contents/%s?ref=%s", pulseGitHubBaseURL, p.cfg.PulseExportRepo, path, p.cfg.PulseExportBranch)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build GET request: %w", err)
	}
	p.setGitHubHeaders(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("GET %s: unexpected status %d: %s", path, resp.StatusCode, string(respBody))
	}

	var parsed githubContentsGetResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode GET %s response: %w", path, err)
	}
	return parsed.SHA, nil
}

func (p *PulseExporter) setGitHubHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+p.cfg.PulseExportToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
}

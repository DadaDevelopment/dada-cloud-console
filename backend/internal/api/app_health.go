package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/auth"
)

// appHealthVerdict is the closed set of facts getAppHealth may return. It is
// deliberately not prose: the assistant reads this to decide what it is
// allowed to tell the user, so every value must be a fact the platform can
// prove, never a guess dressed up as one.
type appHealthVerdict string

const (
	appHealthReady    appHealthVerdict = "ready"
	appHealthNotReady appHealthVerdict = "not_ready"
	appHealthNoSignal appHealthVerdict = "no_signal"
	appHealthStopped  appHealthVerdict = "stopped"
	appHealthOrphaned appHealthVerdict = "orphaned"
	appHealthUnknown  appHealthVerdict = "unknown"
)

// appHealthStaleWindow is the same 10-minute freshness cutoff as
// appSnapshotFreshnessWindow (admin_overview.go): a k8s-sourced snapshot older
// than this cannot be trusted to still describe the live workload, the exact
// gap brokenAppSnapshotPredicate's freshness clause exists to close. Kept as a
// separate Go constant (rather than sharing the SQL string) because this
// handler classifies one already-fetched row in Go instead of running a second
// admin-shaped query; if this window ever changes, appSnapshotFreshnessWindow
// must change with it.
const appHealthStaleWindow = 10 * time.Minute

// appHealthNoSignalGrace is the same 1-hour grace period as
// noSignalAppSnapshotPredicate's first_seen_at cutoff: a workload-less App row
// younger than this is still on its first build, not blind. Keyed on
// first_seen_at, never last_synced_at, for the same reason documented on
// noSignalAppSnapshotPredicate -- last_synced_at is re-stamped every reconcile
// tick and would make a genuinely stale row look perpetually new.
const appHealthNoSignalGrace = time.Hour

// appHealthResponse is the read-only health contract getAppHealth hands to
// both the console UI and the chat assistant. Every field is a fact (a
// verdict derived from the same snapshot predicates admin_overview.go uses, a
// timestamp, a raw platform-reported reason, a log excerpt) rather than an
// opinion, so the assistant can quote it without inventing anything. Note is
// the one field allowed to editorialize, and only to stop silence from being
// misread as health.
type appHealthResponse struct {
	Verdict            appHealthVerdict `json:"verdict"`
	Phase              string           `json:"phase"`
	LiveSource         string           `json:"live_source"`
	SnapshotAgeSeconds int64            `json:"snapshot_age_seconds"`
	SnapshotStale      bool             `json:"snapshot_stale"`
	PlatformReason     string           `json:"platform_reason,omitempty"`
	PlatformReasonAt   string           `json:"platform_reason_at,omitempty"`
	LogExcerpt         []string         `json:"log_excerpt"`
	Note               string           `json:"note,omitempty"`
}

// appSnapshotRow is the subset of resource_snapshots columns classifyAppHealth
// reasons over -- exactly the fields brokenAppSnapshotPredicate and
// noSignalAppSnapshotPredicate branch on.
type appSnapshotRow struct {
	Phase        string
	LiveSource   string
	LastSyncedAt time.Time
	FirstSeenAt  time.Time
}

// GetAppHealth answers "is this app actually broken, or is the platform just
// not looking at it" with facts instead of silence: the same verdict
// admin_overview.go proves for the operator's broken/no-signal panels, plus
// the freshest platform-known failure reason and a collapsed crash-loop log
// tail. It makes no LLM call, which is exactly what makes it safe to hand to
// the console assistant (diagnoseApp is excluded from its allowlist because
// that one calls the AI gateway itself). Read role required.
//
// @ID          getAppHealth
// @Summary     Get an app's health verdict
// @Description Read-only health facts for one app: the same ready/not_ready/no_signal/stopped/orphaned verdict admin_overview.go proves, the platform's freshest known failure reason, and a collapsed log tail. Makes no LLM call. Read role required.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} appHealthResponse
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/health [get]
func (h *Handler) GetAppHealth(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	envID, err := uuid.Parse(c.Param("envId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	appName := c.Param("appName")

	if _, err := h.effectiveRole(c.Request.Context(), claims, projectID); err != nil {
		if err == pgx.ErrNoRows {
			respondNotFound(c)
			return
		}
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return
	} else if !ok {
		respondNotFound(c)
		return
	}

	ctx := c.Request.Context()

	snapshot, found, err := h.fetchAppSnapshot(ctx, projectID, envID, appName)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read app snapshot")
		return
	}

	verdict := appHealthUnknown
	phase := "Unknown"
	liveSource := ""
	var ageSeconds int64
	stale := false
	if found {
		phase = snapshot.Phase
		if phase == "" {
			phase = "Unknown"
		}
		liveSource = snapshot.LiveSource
		verdict, stale = classifyAppHealth(snapshot)
		ageSeconds = int64(time.Since(snapshot.LastSyncedAt).Seconds())
	}

	ns := h.environmentNamespace(ctx, envID)
	reason, reasonAt := h.latestHealthAlertReasonWithTime(ctx, ns, appName)

	entries := h.fetchDiagnoseLogs(ctx, ns, appName)
	lines := buildLogLines(entries)
	collapsed := collapseRepeatedBlocks(lines)
	truncated := truncateLogLines(collapsed, diagnoseMaxLogChars)
	excerpt := lastLines(truncated, diagnoseExcerptLines)
	if excerpt == nil {
		excerpt = []string{}
	}

	c.JSON(http.StatusOK, appHealthResponse{
		Verdict:            verdict,
		Phase:              phase,
		LiveSource:         liveSource,
		SnapshotAgeSeconds: ageSeconds,
		SnapshotStale:      stale,
		PlatformReason:     reason,
		PlatformReasonAt:   reasonAt,
		LogExcerpt:         excerpt,
		Note:               appHealthNote(verdict, stale),
	})
}

// fetchAppSnapshot reads the one resource_snapshots row this handler needs to
// classify, unfiltered by notOrphanedSnapshot: unlike ListApps, this endpoint
// must be able to report Orphaned as an honest verdict rather than hide the
// row. found is false when the app has no snapshot at all (e.g. an
// upload-deploy app whose first build never produced a workload).
func (h *Handler) fetchAppSnapshot(ctx context.Context, projectID, envID uuid.UUID, appName string) (appSnapshotRow, bool, error) {
	var row appSnapshotRow
	err := h.pool.QueryRow(ctx,
		`SELECT rs.phase, COALESCE(rs.summary_json->>'live_source', ''), rs.last_synced_at, rs.first_seen_at
		 FROM resource_snapshots rs
		 WHERE rs.project_id = $1 AND rs.environment_id = $2 AND rs.kind = 'App' AND rs.name = $3`,
		projectID, envID, appName,
	).Scan(&row.Phase, &row.LiveSource, &row.LastSyncedAt, &row.FirstSeenAt)
	if err == pgx.ErrNoRows {
		return appSnapshotRow{}, false, nil
	}
	if err != nil {
		return appSnapshotRow{}, false, err
	}
	return row, true, nil
}

// classifyAppHealth turns one resource_snapshots row into the same verdict
// admin_overview.go's brokenAppSnapshotPredicate and noSignalAppSnapshotPredicate
// would reach for it, reasoned over in Go because this handler already holds
// the row and does not want a second query built from copy-pasted SQL. Any
// future change to either predicate must be mirrored here -- see the comments
// on appHealthStaleWindow and appHealthNoSignalGrace.
//
// Ready/Stopped/Orphaned are settled answers regardless of live_source, same
// as both predicates treat them. A live k8s workload outside those three
// phases is "not_ready" only while its snapshot is fresh (stale=true is
// returned instead, since a stale k8s snapshot cannot be trusted to still
// describe reality). A workload-less row past appHealthNoSignalGrace since
// first_seen_at is "no_signal"; younger than that, or a live k8s row with no
// snapshot age information, is "unknown" -- neither proven broken nor proven
// healthy.
func classifyAppHealth(row appSnapshotRow) (verdict appHealthVerdict, stale bool) {
	switch row.Phase {
	case "Ready":
		return appHealthReady, false
	case "Stopped":
		return appHealthStopped, false
	case "Orphaned":
		return appHealthOrphaned, false
	}

	if row.LiveSource == "k8s" {
		stale = time.Since(row.LastSyncedAt) > appHealthStaleWindow
		if stale {
			return appHealthUnknown, true
		}
		return appHealthNotReady, false
	}

	if time.Since(row.FirstSeenAt) > appHealthNoSignalGrace {
		return appHealthNoSignal, false
	}
	return appHealthUnknown, false
}

// appHealthNote is the one place getAppHealth is allowed to editorialize: a
// silent no_signal or a stale snapshot must never be read as "the app is
// fine" -- exactly the mistake that let macmam@atomicmail.io walk away
// believing "no AppServer attached" was the platform's final word. Empty for
// every verdict that is itself a proven, trustworthy answer.
func appHealthNote(verdict appHealthVerdict, stale bool) string {
	if stale {
		return "Платформа сейчас НЕ ВИДИТ это приложение: последние данные о нём устарели (старше 10 минут). Это не значит, что оно здорово -- сигнала просто нет."
	}
	if verdict == appHealthNoSignal {
		return "Платформа сейчас НЕ ВИДИТ это приложение: от него давно нет ни одного технического сигнала. Это не значит, что оно здорово -- сигнала просто нет."
	}
	return ""
}

// latestHealthAlertReasonWithTime is latestHealthAlertReason plus the
// timestamp the assistant needs to say how fresh the platform's own read is.
// Returns two empty strings when there is no fresh row (including for a
// VM/compose environment where namespace is "").
func (h *Handler) latestHealthAlertReasonWithTime(ctx context.Context, namespace, appName string) (reason, at string) {
	if namespace == "" || h.pool == nil {
		return "", ""
	}
	var (
		reasonVal string
		atVal     time.Time
	)
	err := h.pool.QueryRow(ctx,
		`SELECT COALESCE(reason, ''), COALESCE(last_seen_at, last_sent_at) FROM app_health_alerts
		  WHERE namespace = $1 AND app_name = $2
		    AND COALESCE(last_seen_at, last_sent_at) > now() - make_interval(secs => $3)
		  ORDER BY COALESCE(last_seen_at, last_sent_at) DESC
		  LIMIT 1`,
		namespace, appName, appHealthAlertFreshWindow.Seconds()).Scan(&reasonVal, &atVal)
	if err != nil {
		return "", ""
	}
	return reasonVal, atVal.UTC().Format(time.RFC3339)
}

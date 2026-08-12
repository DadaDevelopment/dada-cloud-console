package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
)

// platformStatusOK / platformStatusDegraded / platformStatusUnknown are the
// closed set of values platformStatusComponent.Status and
// platformStatusResponse.Status may take. Kept as string constants (not an
// enum type) because they cross straight into JSON with no translation step.
const (
	platformStatusOK       = "ok"
	platformStatusDegraded = "degraded"
	platformStatusUnknown  = "unknown"
)

// platformDatabaseVisibilityWindow is how stale db_stat_databases.collected_at
// may get before the console must admit it cannot currently see database
// state at all. The collector that fills this table runs on its own interval
// (db_stats_collector.go); a gap wider than an hour means that collector
// itself has stopped, not that every shard suddenly went healthy, so the
// component reports degraded rather than silently reusing hour-old rows as if
// they were live.
const platformDatabaseVisibilityWindow = time.Hour

// platformFailedBuildsDegradeThreshold is the count of builds that reached
// status='failed' in the last hour above which the signal stops meaning
// "one user's Dockerfile is broken" and starts meaning "something platform-side
// is failing every build" -- a shared base image, a broken buildkit node, a
// registry outage. Below this count a failed build is the ordinary, expected
// outcome of a user pushing bad code and must not be read as a platform
// symptom; dada_builds_failed_recent (metrics/collector.go) alerts on >0 for
// exactly that reason (page a human to look), which is a different bar than
// the one this endpoint needs (tell the model "this is not the user's app").
const platformFailedBuildsDegradeThreshold = 3

// platformStatusComponent is one platform-wide signal platformStatusResponse
// reports. Name is a stable machine id the assistant/UI can key off of, never
// prose; Detail is the one field allowed to carry a sentence, and it is
// scoped to counts and ages only -- see the privacy note on
// platformStatusResponse.
type platformStatusComponent struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// platformStatusResponse is the read-only, LLM-free answer to "is the
// platform itself degraded right now", handed to both the console UI and the
// chat assistant. It exists because the assistant otherwise has no source of
// platform health at all: every other tool it owns (getAppHealth, getAppLogs,
// getAppMetrics) looks inside one tenant's app, so during a platform-wide
// incident the assistant was structurally forced to read a stuck build queue
// or a blind reconciler as evidence against the user's code.
//
// Privacy: this endpoint is reachable by every authenticated user regardless
// of project membership, so no component may ever put a project name, app
// name, email, shard name, or hostname into a response field. Every value is
// a count, an age in seconds/duration, or a verdict string -- nothing that
// identifies a specific tenant or piece of infrastructure. Reviewers adding a
// new component must keep that invariant; TestPlatformStatus_NoTenantIdentifiers
// guards it mechanically for the fields it knows to check, but it cannot
// catch a new leaky field on its own.
type platformStatusResponse struct {
	Status     string                    `json:"status"`
	CheckedAt  string                    `json:"checked_at"`
	Components []platformStatusComponent `json:"components"`
	Note       string                    `json:"note"`
}

const platformStatusDegradedNote = "Сейчас деградирует сама платформа Dada Cloud (см. перечисленные компоненты). Проблема пользователя может быть следствием этого, а не его кода."

const platformStatusOKNote = "Известной деградации платформы сейчас нет. Это не доказательство того, что приложение пользователя здорово: состояние конкретного приложения даёт getAppHealth."

// GetPlatformStatus answers "is the platform itself degraded right now" with
// facts instead of silence, the platform-wide counterpart to GetAppHealth's
// per-app answer. Every component is one cheap read-only SQL query against
// the console's own state tables -- no Kubernetes call, no Prometheus query,
// no LLM call -- so it is safe and cheap to expose to every authenticated
// user, not just admins: the point is that a user hitting a platform-wide
// incident gets the same honest signal an operator would see on
// /admin/overview, without needing admin role. A component whose query fails
// reports unknown with the error's nature in Detail rather than failing the
// whole request; the assistant needs "we could not check X" as a distinct,
// nameable fact, not a 500 it has to guess about.
//
// @ID          getPlatformStatus
// @Summary     Get platform-wide health status
// @Description Read-only, LLM-free platform health signal available to every authenticated user: snapshot reconciler freshness, stuck operations, recent failed builds, database shard state, and database-stats visibility. Never leaks tenant identifiers. Any authenticated role.
// @Tags        platform
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} platformStatusResponse
// @Failure     401 {object} map[string]string
// @Router      /platform/status [get]
func (h *Handler) GetPlatformStatus(c *gin.Context) {
	if _, ok := auth.GetClaims(c); !ok {
		respondUnauthorized(c)
		return
	}

	ctx := c.Request.Context()
	components := []platformStatusComponent{
		h.platformStatusSnapshotReconciler(ctx),
		h.platformStatusStuckOperations(ctx),
		h.platformStatusFailedBuilds(ctx),
		h.platformStatusDatabases(ctx),
		h.platformStatusDatabaseVisibility(ctx),
	}

	status := platformStatusOK
	for _, comp := range components {
		if comp.Status == platformStatusDegraded {
			status = platformStatusDegraded
			break
		}
	}

	note := platformStatusOKNote
	if status == platformStatusDegraded {
		note = platformStatusDegradedNote
	}

	c.JSON(http.StatusOK, platformStatusResponse{
		Status:     status,
		CheckedAt:  time.Now().UTC().Format(time.RFC3339),
		Components: components,
		Note:       note,
	})
}

// platformStatusSnapshotReconciler mirrors overviewNotReadyFreshness's own
// query almost verbatim (admin_overview.go): both need the same fact, "how
// long ago did the App/k8s status reconciler last write anything at all".
// The 10-minute cutoff is appSnapshotFreshnessWindow; it is duplicated here
// as a literal rather than shared because that constant is typed for SQL
// string concatenation in admin_overview.go's own query shape, not for reuse
// as a Go time.Duration -- if appSnapshotFreshnessWindow ever changes, this
// window must change with it by hand, same as appHealthStaleWindow already
// warns for app_health.go.
func (h *Handler) platformStatusSnapshotReconciler(ctx context.Context) platformStatusComponent {
	const freshnessWindow = 10 * time.Minute
	var ageSeconds *float64
	err := h.pool.QueryRow(ctx, `
		SELECT extract(epoch FROM now() - max(rs.last_synced_at))
		FROM resource_snapshots rs
		WHERE rs.kind = 'App' AND rs.summary_json->>'live_source' = 'k8s'`,
	).Scan(&ageSeconds)
	if err != nil {
		return platformStatusComponent{
			Name:   "snapshot_reconciler",
			Status: platformStatusUnknown,
			Detail: "не удалось прочитать возраст снапшотов: ошибка запроса к базе консоли",
		}
	}
	if ageSeconds == nil {
		return platformStatusComponent{Name: "snapshot_reconciler", Status: platformStatusUnknown, Detail: "снапшотов приложений с live_source=k8s пока нет"}
	}
	age := time.Duration(*ageSeconds) * time.Second
	if age > freshnessWindow {
		return platformStatusComponent{
			Name:   "snapshot_reconciler",
			Status: platformStatusDegraded,
			Detail: "реконсилятор состояния приложений не обновлял данные " + age.Round(time.Second).String(),
		}
	}
	return platformStatusComponent{Name: "snapshot_reconciler", Status: platformStatusOK, Detail: "данные о состоянии приложений свежие"}
}

// platformStatusStuckOperations mirrors overviewStuckOperations' predicate
// and threshold exactly (terminalOperationStatuses plus
// WaitingForApproval excluded, stuckOperationThreshold as the age cutoff) so
// this endpoint and /admin/overview can never disagree about what "stuck"
// means.
func (h *Handler) platformStatusStuckOperations(ctx context.Context) platformStatusComponent {
	thresholdSeconds := stuckOperationThreshold.Seconds()
	settled := append(append([]string{}, terminalOperationStatuses...), string(models.OperationStatusWaitingForApproval))

	var count int
	var oldestAgeSeconds *float64
	err := h.pool.QueryRow(ctx, `
		SELECT count(*), max(extract(epoch FROM now() - o.created_at))
		FROM operations o
		WHERE o.status <> ALL($2)
		  AND o.created_at < now() - make_interval(secs => $1)`,
		thresholdSeconds, settled,
	).Scan(&count, &oldestAgeSeconds)
	if err != nil {
		return platformStatusComponent{
			Name:   "operations",
			Status: platformStatusUnknown,
			Detail: "не удалось прочитать число зависших операций: ошибка запроса к базе консоли",
		}
	}
	if count > 0 {
		age := "неизвестного возраста"
		if oldestAgeSeconds != nil {
			age = (time.Duration(*oldestAgeSeconds) * time.Second).Round(time.Second).String()
		}
		return platformStatusComponent{
			Name:   "operations",
			Status: platformStatusDegraded,
			Detail: "зависших операций: " + strconv.Itoa(count) + ", самая старая висит " + age,
		}
	}
	return platformStatusComponent{Name: "operations", Status: platformStatusOK, Detail: "зависших операций нет"}
}

// platformStatusFailedBuilds counts builds that reached status='failed' in
// the last hour, the same window dada_builds_failed_recent
// (metrics/collector.go) uses. That Prometheus gauge alerts on >0 because a
// single failed build already blocks one user's first deploy and is worth a
// human looking at it -- but a single failed build is also the single most
// ordinary outcome of a user pushing broken code, so it must NOT make this
// endpoint tell the assistant "the platform is degraded" and hand the user an
// excuse. platformFailedBuildsDegradeThreshold is the point where the signal
// stops being about one user's Dockerfile and starts looking like a shared
// platform failure (base image, buildkit node, registry).
func (h *Handler) platformStatusFailedBuilds(ctx context.Context) platformStatusComponent {
	var count int
	err := h.pool.QueryRow(ctx, `
		SELECT count(*) FROM builds
		WHERE status = 'failed' AND updated_at > now() - interval '1 hour'`,
	).Scan(&count)
	if err != nil {
		return platformStatusComponent{
			Name:   "builds",
			Status: platformStatusUnknown,
			Detail: "не удалось прочитать число упавших сборок: ошибка запроса к базе консоли",
		}
	}
	if count >= platformFailedBuildsDegradeThreshold {
		return platformStatusComponent{
			Name:   "builds",
			Status: platformStatusDegraded,
			Detail: "упавших сборок за последний час: " + strconv.Itoa(count) + " — похоже на общую проблему сборки, а не на код одного пользователя",
		}
	}
	return platformStatusComponent{Name: "builds", Status: platformStatusOK, Detail: "заметного всплеска упавших сборок нет"}
}

// platformStatusDatabases reports whether any registered shard (db_shards) is
// in a state that is not serving tenants normally. 'draining' is deliberately
// excluded from that: it is the documented data-move state (see
// migrations/105_db_shards.sql) where no NEW database is placed on the shard
// but every existing database on it is still served normally. Counting
// 'draining' as degraded would have made this component permanently red the
// day this handler shipped (shard-1 is draining in production right now) --
// an always-red signal teaches the model to ignore the component entirely,
// which is worse than not having it. Only 'closed' (or any future state that
// is neither 'open' nor 'draining') counts as degraded. Only the count
// crosses the boundary -- never a shard name -- because db_shards.name is
// operator-chosen infrastructure naming that must not leak to every
// authenticated tenant.
func (h *Handler) platformStatusDatabases(ctx context.Context) platformStatusComponent {
	var count int
	err := h.pool.QueryRow(ctx, `SELECT count(*) FROM db_shards WHERE state NOT IN ('open', 'draining')`).Scan(&count)
	if err != nil {
		return platformStatusComponent{
			Name:   "databases",
			Status: platformStatusUnknown,
			Detail: "не удалось прочитать состояние шардов баз данных: ошибка запроса к базе консоли",
		}
	}
	if count > 0 {
		return platformStatusComponent{
			Name:   "databases",
			Status: platformStatusDegraded,
			Detail: "шардов баз данных не в рабочем состоянии: " + strconv.Itoa(count),
		}
	}
	return platformStatusComponent{Name: "databases", Status: platformStatusOK, Detail: "все шарды баз данных в рабочем состоянии"}
}

// platformStatusDatabaseVisibility reports whether db_stats_collector.go's
// own collector is still running, via the freshest db_stat_databases row
// platform-wide. An empty table (nothing collected yet, e.g. a fresh
// environment) reports unknown rather than degraded: that is "we have never
// looked", a different fact from "we used to look and stopped".
func (h *Handler) platformStatusDatabaseVisibility(ctx context.Context) platformStatusComponent {
	var collectedAt *time.Time
	err := h.pool.QueryRow(ctx, `SELECT max(collected_at) FROM db_stat_databases`).Scan(&collectedAt)
	if err != nil {
		return platformStatusComponent{
			Name:   "database_visibility",
			Status: platformStatusUnknown,
			Detail: "не удалось прочитать свежесть статистики баз данных: ошибка запроса к базе консоли",
		}
	}
	if collectedAt == nil {
		return platformStatusComponent{Name: "database_visibility", Status: platformStatusUnknown, Detail: "статистика баз данных ещё ни разу не собиралась"}
	}
	age := time.Since(*collectedAt)
	if age > platformDatabaseVisibilityWindow {
		return platformStatusComponent{
			Name:   "database_visibility",
			Status: platformStatusDegraded,
			Detail: "состояние баз мы сейчас не видим: последний сбор статистики был " + age.Round(time.Second).String() + " назад",
		}
	}
	return platformStatusComponent{Name: "database_visibility", Status: platformStatusOK, Detail: "статистика баз данных актуальна"}
}

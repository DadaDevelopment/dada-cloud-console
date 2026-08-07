package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/dada-tuda/console/backend/internal/solutions"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// legacyDemoTemplateRepos is the set of platform-owned starter repositories the
// console used to offer as its one-click showroom. They are retired in favour of
// the ready-made project catalog (internal/solutions), but they stay listed here
// because apps deployed from them are still out there with a deadline stamped on
// them, and dropping the entry would strand those apps in the projects of people
// who never claimed them. Membership is an exact match on the full name so a
// user repository that merely ends in "-starter" is never treated as disposable.
var legacyDemoTemplateRepos = map[string]struct{}{
	"DadaDevelopment/dada-nextjs-starter":  {},
	"DadaDevelopment/dada-fastapi-starter": {},
	"DadaDevelopment/dada-static-starter":  {},
}

// isDemoTemplateRepo reports whether an app linked to repoFullName is a showroom
// deploy rather than the customer's own work: a retired starter, or one of the
// catalog's open-source projects.
//
// The catalog half is what keeps the reaper honest after the starters are gone.
// A catalog project is something the platform offered on an empty screen, and
// the one that nobody claims is exactly the app that used to sit Ready for
// eighteen days in a project whose owner never deployed anything of their own. A
// repository the customer pasted themselves is never a demo — they chose it, and
// putting their choice on a timer would be a different product.
//
// Case-insensitive because GitHub owner/repo names are.
func isDemoTemplateRepo(repoFullName string) bool {
	for known := range legacyDemoTemplateRepos {
		if strings.EqualFold(known, repoFullName) {
			return true
		}
	}
	return solutions.IsCatalogRepo(repoFullName)
}

// demoAppTTL is the deadline stamped on a starter-template app at link time.
// Zero means the reaper is off and no deadline is stamped at all, so turning
// DEMO_APP_TTL_HOURS to 0 stops future demos expiring as well as stopping the
// deletion of the ones already stamped.
func (h *Handler) demoAppTTL() time.Duration {
	if h.cfg == nil {
		return 0
	}
	return time.Duration(h.cfg.DemoAppTTLHours) * time.Hour
}

// demoExpiryFor returns the deadline to store for a newly linked repository, or
// nil when the repository is not a demo (or the reaper is disabled).
func (h *Handler) demoExpiryFor(repoFullName string) *time.Time {
	ttl := h.demoAppTTL()
	if ttl <= 0 || !isDemoTemplateRepo(repoFullName) {
		return nil
	}
	deadline := time.Now().Add(ttl)
	return &deadline
}

// FillDemoExpiry copies each git_repos row's deletion deadline onto the app
// snapshot of the same name, so the console can show the "demo, deleted in N h"
// badge and its claim button without a second round trip.
func FillDemoExpiry(apps []models.ResourceSnapshot, rows []GitRepoRow) {
	deadlines := make(map[string]*time.Time, len(rows))
	for _, r := range rows {
		if r.DemoExpiresAt != nil {
			deadlines[r.Name] = r.DemoExpiresAt
		}
	}
	if len(deadlines) == 0 {
		return
	}
	for i := range apps {
		if d, ok := deadlines[apps[i].Name]; ok {
			apps[i].DemoExpiresAt = d
		}
	}
}

const demoAppReapInterval = 15 * time.Minute

// StartDemoAppReaper launches the loop that deletes expired starter-template
// demos. No-op when DEMO_APP_TTL_HOURS is 0, so an operator can stop automatic
// deletion without a code change, and tests never spawn it.
//
// The delete goes through the same operations row the DeleteApp handler writes,
// so gitops-agent runs the identical cascade: nothing here reaches into the
// cluster or the gitops repository on its own.
func (h *Handler) StartDemoAppReaper(ctx context.Context) {
	if h.demoAppTTL() <= 0 {
		log.Info().Msg("demo-reaper: DEMO_APP_TTL_HOURS=0, reaper disabled")
		return
	}
	go func() {
		runWithAdvisoryLock(ctx, h.pool, lockKeyDemoAppReap, "demo-reaper", h.reapExpiredDemoApps)
		t := time.NewTicker(demoAppReapInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runWithAdvisoryLock(ctx, h.pool, lockKeyDemoAppReap, "demo-reaper", h.reapExpiredDemoApps)
			}
		}
	}()
}

type expiredDemo struct {
	projectID uuid.UUID
	envID     uuid.UUID
	appName   string
	repo      string
}

// reapExpiredDemoApps enqueues a DeleteApp operation for every demo past its
// deadline and clears the deadline in the same transaction as the enqueue, so a
// slow gitops cascade cannot make the next tick enqueue the same delete twice.
func (h *Handler) reapExpiredDemoApps(ctx context.Context) {
	rows, err := h.pool.Query(ctx,
		`SELECT project_id, environment_id, app_name, repo_full_name
		   FROM git_repos
		  WHERE demo_expires_at IS NOT NULL AND demo_expires_at <= now()`)
	if err != nil {
		log.Warn().Err(err).Msg("demo-reaper: query expired demos failed")
		return
	}
	var expired []expiredDemo
	for rows.Next() {
		var d expiredDemo
		if scanErr := rows.Scan(&d.projectID, &d.envID, &d.appName, &d.repo); scanErr != nil {
			continue
		}
		expired = append(expired, d)
	}
	rows.Close()
	if rows.Err() != nil {
		log.Warn().Err(rows.Err()).Msg("demo-reaper: reading expired demos failed")
		return
	}

	for _, d := range expired {
		if err := h.enqueueDemoDelete(ctx, d); err != nil {
			log.Warn().Err(err).
				Str("app", d.appName).
				Str("project", d.projectID.String()).
				Msg("demo-reaper: enqueue delete failed")
			continue
		}
		log.Info().
			Str("app", d.appName).
			Str("repo", d.repo).
			Str("project", d.projectID.String()).
			Msg("demo-reaper: expired demo app queued for deletion")
	}
}

func (h *Handler) enqueueDemoDelete(ctx context.Context, d expiredDemo) error {
	payloadBytes, err := json.Marshal(models.DeleteAppPayload{Name: d.appName})
	if err != nil {
		return err
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var opID uuid.UUID
	if err := tx.QueryRow(ctx,
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'DeleteApp', 'App', $4, 'Created', $5)
		 RETURNING id`,
		systemDeployActorID, d.projectID, d.envID, d.appName, payloadBytes,
	).Scan(&opID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE git_repos SET demo_expires_at = NULL, updated_at = now()
		  WHERE project_id = $1 AND environment_id = $2 AND app_name = $3`,
		d.projectID, d.envID, d.appName,
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	h.recordSystemAudit(ctx, auditEntry{
		ProjectID:     d.projectID,
		EnvironmentID: d.envID,
		OperationID:   opID,
		Action:        "DeleteApp",
		ResourceKind:  "App",
		ResourceName:  d.appName,
		Outcome:       auditOutcomeSuccess,
		Metadata:      map[string]any{"reason": "demo_expired", "repo": d.repo},
	})
	return nil
}

// KeepDemoApp claims a starter-template demo as the user's own, clearing its
// deletion deadline.
//
// @ID          keepDemoApp
// @Summary     Keep a demo app
// @Description Cancels the automatic deletion of an app deployed from a platform starter template, so it survives like any other app. A no-op on apps that were never demos. Requires write access.
// @Tags        apps
// @Produce     json
// @Param       projectId path string true "Project ID"
// @Param       envId     path string true "Environment ID"
// @Param       appName   path string true "App name"
// @Success     200 {object} map[string]string
// @Failure     401 {object} map[string]string
// @Failure     403 {object} map[string]string
// @Failure     404 {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/keep [post]
// @Security    BearerAuth
func (h *Handler) KeepDemoApp(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	appName := c.Param("appName")

	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		return
	}
	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return
	} else if !ok {
		respondNotFound(c)
		return
	}

	tag, err := h.pool.Exec(c.Request.Context(),
		`UPDATE git_repos SET demo_expires_at = NULL, updated_at = now()
		  WHERE project_id = $1 AND environment_id = $2 AND app_name = $3`,
		projectID, envID, appName)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to keep app")
		return
	}
	if tag.RowsAffected() == 0 {
		respondNotFound(c)
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        "KeepDemoApp",
		ResourceKind:  "App",
		ResourceName:  appName,
		Outcome:       auditOutcomeSuccess,
	})

	c.JSON(http.StatusOK, gin.H{"message": "app kept"})
}

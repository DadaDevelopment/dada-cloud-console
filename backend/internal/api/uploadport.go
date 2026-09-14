package api

import (
	"net/http"
	"strconv"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type setUploadPortRequest struct {
	Port   *int `json:"port"`
	Worker bool `json:"worker"`
}

// resolveUploadPort turns a setUploadPortRequest into the (port, worker) pair
// SetUploadPort writes to git_repos, or a non-empty errCode/errMsg pair when
// the request is invalid. It is split out of the handler as a pure function
// so the validation contract (worker=true always wins; otherwise port must be
// present and in 1..65535) is unit-testable without TEST_DATABASE_URL, the
// same way UpdateAppPort's range check at apps.go:1703-1706 has no DB
// dependency of its own even though the handler around it does.
//
// worker=true short-circuits before the port is even inspected, matching
// UpdateAppPort's rule that typing a port is what asserts "this app serves
// HTTP" (apps.go:1637-1645): here the mirror statement, worker=true, is what
// the user is asserting when they tick that box, regardless of whatever port
// value happens to be sitting in the request body from a stale form.
func resolveUploadPort(req setUploadPortRequest) (port int, worker bool, errCode, errMsg string) {
	if req.Worker {
		return 0, true, "", ""
	}
	if req.Port == nil || *req.Port < 1 || *req.Port > 65535 {
		return 0, false, "invalid_port", "port must be between 1 and 65535"
	}
	return *req.Port, false, "", ""
}

// SetUploadPort writes the web-port-or-worker verdict onto an UPLOADED
// archive's git_repos row (provider='archive') before the app has ever been
// deployed, closing the gap left by UploadSourceArchive's own guess.
//
// UploadSourceArchive (uploadsource.go) detects a framework and port from the
// archive's manifest files and, when detection finds nothing listening
// (isWorkerUpload, port<=0), stamps worker=true and a nominal port=8080 onto
// the row so the chart has something to render. That guess is frequently
// wrong in both directions: a Dockerfile with no EXPOSE that actually runs a
// web server reads as a worker, and a framework misdetection can hand a real
// HTTP app the wrong port. The existing lever for a wrong port,
// PATCH .../apps/:appName/port -> UpdateAppPort (apps.go:1663), edits
// resource_snapshots.summary_json for a row that UpdateAppPort itself reads
// with `SELECT ... FROM resource_snapshots WHERE kind = 'App'` (apps.go:1717-
// 1722) — and that row does not exist yet. CreateApp/HandoffDeploy is what
// first inserts it, and HandoffDeploy (build-agent/internal/db/deploy.go:221)
// only runs after this upload's queued build finishes successfully. Between
// upload and first successful build there is a window, often the very first
// thing the user sees after uploading, where the console can show the
// detected verdict but has nowhere durable to write a correction.
//
// This endpoint is that write target: it updates git_repos directly, the same
// row isWorkerUpload's result was stamped onto, and the same row HandoffDeploy
// reads (repo.Port, repo.Worker at build-agent/internal/db/deploy.go:280 and
// workerPort/workerReplicas) to decide the app's initial port, Service and
// whether it gets a default hostname at all. A worker app never gets a
// surrogate domain (`dd.Enabled && dd.Base != "" && !hasManagedDomain &&
// !repo.Worker`, deploy.go:280): if detection's worker=true guess was wrong,
// this is the only way to flip it before that decision is made, because after
// the first successful build HandoffDeploy has already run and the app's
// hostname (or lack of one) is baked in.
//
// It intentionally does NOT call h.requireK8sRuntime the way UpdateAppPort
// does. That guard reads environments.runtime and rejects non-Kubernetes
// environments, which is correct for UpdateAppPort because it is editing a
// resource_snapshots row that only exists for a k8s-rendered App. Here there
// is no App yet at all — git_repos rows are written for every runtime the
// upload flow supports, and HandoffDeploy (not this endpoint) is what later
// branches on runtime when it materializes the app. Gating on k8s runtime here
// would reject the exact pre-build correction this endpoint exists for on any
// environment where that decision has not been made yet.
//
// Semantics mirror UpdateAppPort's worker-clearing rule for symmetry across
// the two lifecycle stages, but run in the opposite direction as well: worker
// true sets git_repos.worker=true and leaves port untouched (the nominal 8080
// UploadSourceArchive already wrote stays as a harmless placeholder, since
// workerPort zeroes it at deploy time regardless of what is stored); a port in
// 1..65535 sets git_repos.port=<port> and clears worker=false, because typing
// a port is the user asserting this app serves HTTP, exactly as it is for
// UpdateAppPort (apps.go:1637-1645). A request that sets neither, or an
// out-of-range port, is rejected with code "invalid_port" using the same
// respondErrorCode contract as UpdateAppPort (apps.go:1703-1706) and
// linkGitRepo (gitrepos.go:1459-1461), so the frontend's existing
// code==='invalid_port' branch keeps working unmodified for this endpoint too.
//
// Only a provider='archive' row is ever touched (the WHERE clause below), and
// a git-linked repo, or an app that already has no archive row, gets a plain
// 404 — this endpoint must never be usable to rewrite a GitHub/GitLab binding
// the way the upload UPSERT itself refuses to (see the comment at
// uploadsource.go:241-247 on why that door stays one-way in the other
// direction, and why UploadSourceArchive's own UPSERT is scoped the same way).
//
// Writing git_repos alone is NOT enough and was the first version of this
// handler: for an archive build the build-agent does not read the app's row as
// stored. sourceForBuild overlays it per build, and
// `if b.ArchivePort != nil && *b.ArchivePort > 0 { src.Port = *b.ArchivePort }`
// (build-agent/internal/worker/runner.go:1892-1893) puts the upload-time value
// back on top of whatever git_repos now says. UploadSourceArchive stores the
// nominal 8080 in builds.archive_port for a portless upload
// (uploadsource.go:236-239), so that overlay would clobber the port the user
// just typed, and the archive branch at runner.go:1355-1360 would forward the
// stale value to Jenkins as app_port as well. The still-unfinished build of
// this app is therefore updated in the SAME transaction, which is also the
// only build that can still act on the correction: statuses beyond
// 'pushing' have already handed off to HandoffDeploy. Terminal builds are
// deliberately left alone so this never rewrites the history of a deploy that
// already happened.
//
// @ID          setUploadPort
// @Summary     Set the web port or worker flag for an uploaded archive app
// @Description Writes the port-or-worker verdict onto an uploaded archive's git_repos row before its first build deploys, correcting a wrong framework-detection guess (wrong port, or worker misdetected either direction) before HandoffDeploy reads it to decide the app's Service and default hostname. Only touches provider='archive' rows.
// @Tags        build
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                true "Project UUID"
// @Param       envId     path     string                true "Environment UUID"
// @Param       appName   path     string                true "App name"
// @Param       body      body     setUploadPortRequest  true "New port or worker confirmation"
// @Success     200       {object} map[string]interface{}
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/upload-port [patch]
func (h *Handler) SetUploadPort(c *gin.Context) {
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

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	if !canWrite(role) {
		respondForbidden(c)
		return
	}

	var req setUploadPortRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErrorCode(c, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}

	port, worker, errCode, errMsg := resolveUploadPort(req)
	if errCode != "" {
		respondErrorCode(c, http.StatusBadRequest, errCode, errMsg)
		return
	}

	ctx := c.Request.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to update uploaded source")
		return
	}
	defer tx.Rollback(ctx)

	var gitRepoID uuid.UUID
	if worker {
		err = tx.QueryRow(ctx,
			`UPDATE git_repos SET worker = TRUE, updated_at = NOW()
			 WHERE project_id = $1 AND environment_id = $2 AND app_name = $3 AND provider = 'archive'
			 RETURNING id`,
			projectID, envID, appName,
		).Scan(&gitRepoID)
	} else {
		err = tx.QueryRow(ctx,
			`UPDATE git_repos SET port = $4, worker = FALSE, updated_at = NOW()
			 WHERE project_id = $1 AND environment_id = $2 AND app_name = $3 AND provider = 'archive'
			 RETURNING id`,
			projectID, envID, appName, port,
		).Scan(&gitRepoID)
	}
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to update uploaded source")
		return
	}

	if !worker {
		if _, err := tx.Exec(ctx,
			`UPDATE builds SET archive_port = $2, updated_at = NOW()
			 WHERE git_repo_id = $1 AND archive_url IS NOT NULL
			   AND status IN ('queued', 'detecting', 'building', 'pushing')`,
			gitRepoID, port,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to update uploaded source")
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to update uploaded source")
		return
	}

	auditMetadata := map[string]any{"worker": worker}
	if !worker {
		auditMetadata["port"] = strconv.Itoa(port)
	}
	h.recordAudit(ctx, claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        "SetUploadPort",
		ResourceKind:  "Build",
		ResourceName:  appName,
		Outcome:       auditOutcomeSuccess,
		Metadata:      auditMetadata,
	})

	resp := gin.H{"worker": worker}
	if !worker {
		resp["port"] = port
	}
	c.JSON(http.StatusOK, resp)
}

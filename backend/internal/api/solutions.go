package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/crypto"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/dada-tuda/console/backend/internal/solutions"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Ready-made solutions: install a whole third-party product onto a VM app
// server in one click.
//
// The install is a thin, deliberate layer over the ordinary compose-app path.
// It resolves a frozen catalog entry (internal/solutions) into N first-class
// Applications, mints the credentials the product needs, writes every value into
// env_vars encrypted, and enqueues ONE InstallSolution operation. From the
// moment that operation is committed there is nothing special about the result:
// the apps have per-app logs, metrics, env editing, restart and delete, because
// they ARE ordinary apps. Nothing here reaches into the cluster, the VM or the
// gitops repository — the worker does all of that, as it does for every other
// mutation.

// solutionPayload is the wire shape of one catalog entry. Rendered by hand
// rather than by marshalling solutions.Solution so the API surface stays a
// deliberate decision: internal fields (image refs, digests, the bootstrap
// command) never leak into a response just because someone added a field.
func solutionPayload(s solutions.Solution) gin.H {
	services := make([]gin.H, 0, len(s.Services))
	for _, svc := range s.Services {
		services = append(services, gin.H{
			"name_suffix": svc.NameSuffix,
			"description": svc.Description,
			"ports":       svc.Ports,
			"primary":     svc.Primary,
		})
	}
	params := make([]gin.H, 0, len(s.Params))
	for _, p := range s.Params {
		params = append(params, gin.H{
			"key":         p.Key,
			"label":       p.Label,
			"help":        p.Help,
			"kind":        string(p.Kind),
			"required":    p.Required,
			"default":     p.Default,
			"options":     p.Options,
			"placeholder": p.Placeholder,
		})
	}
	generated := make([]gin.H, 0, len(s.Secrets))
	for _, g := range s.Secrets {
		generated = append(generated, gin.H{"key": g.Key, "label": g.Label})
	}
	return gin.H{
		"slug":          s.Slug,
		"name":          s.Name,
		"tagline":       s.Tagline,
		"about":         s.About,
		"bullets":       s.Bullets,
		"category":      string(s.Category),
		"vendor":        s.Vendor,
		"homepage":      s.Homepage,
		"license":       s.License,
		"docs_slug":     s.DocsSlug,
		"min_vcpu":      s.MinVCPU,
		"min_memory_mb": s.MinMemoryMB,
		"min_disk_gb":   s.MinDiskGB,
		"warning":       s.Warning,
		"installable":   s.Pinned(),
		"params":        params,
		"generated":     generated,
		"services":      services,
	}
}

// ListSolutions returns the ready-made solution catalog.
//
// @ID          listSolutions
// @Summary     List ready-made solutions
// @Description Returns the catalog of ready-made solutions installable onto a VM app server in one click. Entries whose image is not published yet are listed with installable=false. Read-only; the catalog is the same for every project.
// @Tags        solutions
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string]interface{} "object with a solutions array"
// @Failure     401 {object} map[string]string
// @Router      /solutions [get]
func (h *Handler) ListSolutions(c *gin.Context) {
	if _, ok := auth.GetClaims(c); !ok {
		respondUnauthorized(c)
		return
	}
	out := make([]gin.H, 0, len(solutions.V1))
	for _, s := range solutions.V1 {
		out = append(out, solutionPayload(s))
	}
	c.JSON(http.StatusOK, gin.H{"solutions": out})
}

// GetSolution returns one catalog entry by slug.
//
// @ID          getSolution
// @Summary     Get one ready-made solution
// @Description Returns a single catalog entry, including the parameters its install asks for and the credentials the platform generates. Read-only.
// @Tags        solutions
// @Produce     json
// @Security    BearerAuth
// @Param       slug path     string true "Solution slug"
// @Success     200  {object} map[string]interface{}
// @Failure     401  {object} map[string]string
// @Failure     404  {object} map[string]string
// @Router      /solutions/{slug} [get]
func (h *Handler) GetSolution(c *gin.Context) {
	if _, ok := auth.GetClaims(c); !ok {
		respondUnauthorized(c)
		return
	}
	s, ok := solutions.Lookup(c.Param("slug"))
	if !ok {
		respondNotFound(c)
		return
	}
	c.JSON(http.StatusOK, solutionPayload(s))
}

type installSolutionRequest struct {
	// Name is the install's instance name. It becomes the primary app's name and
	// the prefix of every other app and volume the install creates. Empty falls
	// back to the solution slug.
	Name   string            `json:"name"`
	Params map[string]string `json:"params"`
}

// generateSecretValue mints one platform-generated credential.
//
// Hex rather than base64url: a dashboard password is read off a screen and typed
// into a login form, and hex has no characters a human can misread as another
// character or a shell can treat as special.
func generateSecretValue(kind solutions.GeneratedKind) (string, error) {
	size := 16
	if kind == solutions.GeneratedSecret {
		size = 32
	}
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// InstallSolution installs one catalog solution onto a VM environment.
//
// @ID          installSolution
// @Summary     Install a ready-made solution
// @Description Installs a catalog solution onto the VM environment's app server as one or more Applications. Requires a VM (compose) environment whose app server is Ready. Parameters are validated against the catalog entry; credentials the platform generates are returned once in the response and stored encrypted. Asynchronous: returns 202 with an operation; poll the operation until terminal.
// @Tags        solutions
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                 true "Project UUID"
// @Param       envId     path     string                 true "Environment UUID"
// @Param       slug      path     string                 true "Solution slug"
// @Param       body      body     installSolutionRequest true "Install parameters"
// @Success     202       {object} map[string]interface{} "object with the accepted operation, the created app names and any generated credentials"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/solutions/{slug} [post]
func (h *Handler) InstallSolution(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
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

	slug := c.Param("slug")
	sol, known := solutions.Lookup(slug)
	if !known {
		respondNotFound(c)
		return
	}

	var req installSolutionRequest
	instance := slug

	reject := func(status int, reason, msg string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "InstallSolution",
			ResourceKind:  "App",
			ResourceName:  instance,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"solution": slug, "reason": reason, "status": status},
		})
		respondError(c, status, msg)
	}

	// An entry whose image has not been published yet is visible in the catalog
	// but refuses to install: resolving a one-click button to a tag that does not
	// exist would fail deep inside a Portainer image pull, with an error nobody
	// outside the platform team can read.
	if !sol.Pinned() {
		reject(http.StatusConflict, "image_not_published",
			"this solution is not available for installation yet")
		return
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		reject(http.StatusBadRequest, "malformed_body", err.Error())
		return
	}
	if req.Name != "" {
		instance = req.Name
	}
	if err := solutions.ValidateInstanceName(instance); err != nil {
		reject(http.StatusBadRequest, "invalid_name", err.Error())
		return
	}

	// The environment must be a VM (compose) environment with a Ready app server,
	// checked exactly as CreateApp checks it: a solution is a compose stack, and
	// there is nowhere to put one otherwise.
	var runtime models.EnvironmentRuntime
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT runtime FROM environments WHERE id = $1 AND project_id = $2`,
		envID, projectID,
	).Scan(&runtime); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load environment runtime")
		return
	}
	if runtime != models.EnvironmentRuntimeVM {
		reject(http.StatusConflict, "not_a_vm_environment",
			"ready-made solutions install onto a VM environment; this environment runs on Kubernetes")
		return
	}
	var serverStatus string
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT s.status FROM environments e JOIN app_servers s ON s.id = e.app_server_id
		 WHERE e.id = $1 AND e.project_id = $2`,
		envID, projectID,
	).Scan(&serverStatus); err == pgx.ErrNoRows {
		reject(http.StatusConflict, "no_appserver",
			"this VM environment has no AppServer attached; create or attach one first")
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load environment AppServer")
		return
	}
	if serverStatus != string(models.AppServerStatusReady) {
		reject(http.StatusConflict, "appserver_not_ready", "the environment's AppServer is not Ready yet")
		return
	}

	if orgID, orgErr := h.projectOrg(c.Request.Context(), projectID); orgErr == nil {
		if qErr := h.checkQuota(c.Request.Context(), orgID, "apps"); qErr != nil {
			if meta, blocked := billingBlockAudit(qErr); blocked {
				h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
					ProjectID:     projectID,
					EnvironmentID: envID,
					Action:        "InstallSolution",
					ResourceKind:  "App",
					ResourceName:  instance,
					Outcome:       auditOutcomeFailure,
					Metadata:      meta,
				})
				h.respondBillingBlocked(c, orgID, qErr)
				return
			}
		}
	}

	// Every app the install would create must be free. Checked up front, for all
	// of them, because a partial install is worse than a refused one: half a
	// solution is a stack that starts, fails and leaves the customer to work out
	// which half is theirs.
	appNames := make([]string, 0, len(sol.Services))
	for _, svc := range sol.Services {
		name := svc.AppName(instance)
		if err := validateKubeName(name); err != nil {
			reject(http.StatusBadRequest, "invalid_name", err.Error())
			return
		}
		var exists int
		err := h.pool.QueryRow(c.Request.Context(),
			`SELECT 1 FROM resource_snapshots
			  WHERE kind = 'App' AND name = $1 AND project_id = $2 AND environment_id = $3
			  LIMIT 1`,
			name, projectID, envID,
		).Scan(&exists)
		if err == nil {
			reject(http.StatusConflict, "name_taken", fmt.Sprintf(
				"the app name %q is already taken in this environment; choose another install name", name))
			return
		} else if err != pgx.ErrNoRows {
			respondError(c, http.StatusInternalServerError, "failed to check name uniqueness")
			return
		}
		appNames = append(appNames, name)
	}

	env, err := sol.ResolveParams(req.Params)
	if err != nil {
		reject(http.StatusBadRequest, "invalid_params", err.Error())
		return
	}
	// Which env keys hold something that must never be echoed back. Non-secret
	// values (an endpoint URL, a model name) stay readable in the app's env
	// editor, so a customer can see how their install is configured without an
	// audited reveal.
	secretEnvKeys := map[string]bool{}
	for _, p := range sol.Params {
		if p.Kind == solutions.ParamSecret {
			secretEnvKeys[p.EnvKey] = true
		}
	}
	revealed := map[string]string{}
	revealKeys := map[string]bool{}
	for _, k := range sol.RevealKeys {
		revealKeys[k] = true
	}
	for _, g := range sol.Secrets {
		value, genErr := generateSecretValue(g.Kind)
		if genErr != nil {
			respondError(c, http.StatusInternalServerError, "failed to generate credentials")
			return
		}
		env[g.EnvKey] = value
		secretEnvKeys[g.EnvKey] = true
		if revealKeys[g.Key] {
			revealed[g.Key] = value
		}
	}

	apps := make([]models.SolutionAppSpec, 0, len(sol.Services))
	for _, svc := range sol.Services {
		apps = append(apps, models.SolutionAppSpec{
			Name:    svc.AppName(instance),
			Image:   svc.ImageRef(),
			Command: svc.Command,
			Ports:   svc.Ports,
			Volumes: prefixVolumes(instance, svc.Volumes),
		})
	}
	payloadBytes, err := json.Marshal(models.InstallSolutionPayload{
		Slug:     sol.Slug,
		Instance: instance,
		Apps:     apps,
	})
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to marshal payload")
		return
	}

	tx, err := h.pool.Begin(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	// The whole env set goes to every app of the install. The services of a
	// solution are one product sharing one data volume, and splitting the
	// variables per service would only mean guessing which half of a third-party
	// product reads which variable.
	for _, name := range appNames {
		for key, value := range env {
			if err := h.seedEnvVarTx(c.Request.Context(), tx, envID, name, key, value,
				secretEnvKeys[key], claims.UserID); err != nil {
				respondError(c, http.StatusInternalServerError, "failed to store install parameters")
				return
			}
		}
	}

	var op models.Operation
	row := tx.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'InstallSolution', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, instance, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	for _, app := range apps {
		if err := seedOptimisticSnapshot(c.Request.Context(), tx, projectID, envID, "App", app.Name,
			map[string]any{"runtime": "compose", "solution": sol.Slug, "image": app.Image},
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to create operation")
			return
		}
	}

	if err := tx.Commit(c.Request.Context()); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "InstallSolution",
		ResourceKind:  "App",
		ResourceName:  instance,
		Outcome:       auditOutcomeSuccess,
		// Never the values: an audit row is read by more people than the env
		// editor is, and the whole point of the reveal path is that reading a
		// secret is itself an audited act.
		Metadata: map[string]any{"solution": sol.Slug, "apps": appNames},
	})
	h.notifyAuditEvent(claims, projectID, "InstallSolution", instance)

	c.JSON(http.StatusAccepted, gin.H{
		"operation":   op,
		"apps":        appNames,
		"credentials": revealed,
		"message":     "solution install queued",
	})
}

// prefixVolumes namespaces a solution's named volumes with the install name, so
// two installs of the same solution onto one VM never mount each other's data.
// A bind mount (an absolute host path) is passed through untouched — it is
// already an explicit choice about a specific host directory.
func prefixVolumes(instance string, vols []string) []string {
	if len(vols) == 0 {
		return nil
	}
	out := make([]string, 0, len(vols))
	for _, v := range vols {
		if len(v) > 0 && v[0] == '/' {
			out = append(out, v)
			continue
		}
		out = append(out, instance+"-"+v)
	}
	return out
}

// seedEnvVarTx is seedEnvVar inside a caller's transaction, with an explicit
// secret flag. The install writes every variable and the operation row in ONE
// transaction: a crash between them would otherwise leave an app whose
// credentials exist but whose stack was never rendered, or the reverse — a
// container booting without the password it fails closed without.
func (h *Handler) seedEnvVarTx(ctx context.Context, tx pgx.Tx, envID uuid.UUID, appName, key, value string, isSecret bool, createdBy uuid.UUID) error {
	enc, err := crypto.EncryptToken(h.cfg.GitopsEncryptionKey, []byte(value))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO env_vars (environment_id, app_name, key, value_encrypted, is_secret, scope, created_by)
		 VALUES ($1, $2, $3, $4, $5, 'runtime', $6)
		 ON CONFLICT (environment_id, app_name, key)
		 DO UPDATE SET value_encrypted = EXCLUDED.value_encrypted, is_secret = EXCLUDED.is_secret,
		               scope = EXCLUDED.scope, updated_at = NOW()`,
		envID, appName, key, enc, isSecret, createdBy,
	)
	return err
}

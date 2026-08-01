package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FillRepoFullName injects repo_full_name (from git_repos, keyed by app name)
// into each app's summary when the snapshot summary does not already carry it.
// A deployed app's resource-snapshot summary omits the linked repo, so without
// this the UI shows "not linked" even though the repo is connected in git_repos.
func FillRepoFullName(apps []models.ResourceSnapshot, repoByName map[string]string) {
	for i := range apps {
		repo := repoByName[apps[i].Name]
		if repo == "" {
			continue
		}
		var m map[string]any
		if len(apps[i].SummaryJSON) > 0 {
			_ = json.Unmarshal(apps[i].SummaryJSON, &m)
		}
		if m == nil {
			m = map[string]any{}
		}
		if cur, _ := m["repo_full_name"].(string); cur == "" {
			m["repo_full_name"] = repo
			if b, err := json.Marshal(m); err == nil {
				apps[i].SummaryJSON = b
			}
		}
	}
}

// FillEffectiveResources adds the resource envelope an app actually runs with
// to its summary, so the console can show real numbers for every app instead of
// a profile name.
//
// Apps sized by the autoscaler already carry "resources"; an app that has never
// been resized carries only the legacy profile name, and its numbers live in
// the gitops-agent renderer. This resolves that fallback on the read path only —
// nothing is written back, so an unsized app stays unsized and keeps following
// the renderer's defaults if those ever move.
func FillEffectiveResources(apps []models.ResourceSnapshot) {
	for i := range apps {
		var m map[string]any
		if len(apps[i].SummaryJSON) > 0 {
			_ = json.Unmarshal(apps[i].SummaryJSON, &m)
		}
		if m == nil {
			continue
		}
		if _, ok := m["resources"]; ok {
			continue
		}
		profile, _ := m["profile"].(string)
		if profile == "" {
			profile = "small"
		}
		envelope, ok := autoscaleProfileRequirements[profile]
		if !ok {
			continue
		}
		m["resources"] = envelope.snapshot()
		if b, err := json.Marshal(m); err == nil {
			apps[i].SummaryJSON = b
		}
	}
}

// SuppressNonHTTPURL blanks the summary "url" field for apps whose stored
// port fails servesHTTP (the same gate CreateApp uses to skip the auto
// surrogate domain). A resource_snapshots row can carry a stale "url" set by
// the status reconciler before the port was known to be a datastore port, or
// from before servesHTTP existed; showing that URL as the app's live
// endpoint is always wrong for a raw-TCP service (nginx cannot speak HTTP to
// redis/postgres/etc, guaranteed 502). Ambiguous cases (no numeric "port" in
// the summary) are left untouched so ordinary web apps never regress.
func SuppressNonHTTPURL(apps []models.ResourceSnapshot) {
	for i := range apps {
		if len(apps[i].SummaryJSON) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(apps[i].SummaryJSON, &m); err != nil {
			continue
		}
		if _, hasURL := m["url"]; !hasURL {
			continue
		}
		portVal, ok := m["port"].(float64)
		if !ok {
			continue
		}
		if servesHTTP(int(portVal)) {
			continue
		}
		delete(m, "url")
		if b, err := json.Marshal(m); err == nil {
			apps[i].SummaryJSON = b
		}
	}
}

// placeholderImagePrefix is the image the console deploys for an app whose real
// image does not exist yet: the upload flow must create the App before it can
// POST the archive to it, so it seeds a pause container as a stand-in. The pause
// container starts instantly and never exits, and console-created apps carry no
// liveness/readiness probes, so kubelet reports it 1/1 Running within seconds and
// the status reconciler writes phase=Ready. Everything downstream then lies: the
// app list shows a green Ready badge, the detail page offers the default domain
// (which 502s — pause serves nothing), and the frontend fires the
// deploy_success Metrika goal, so the funnel counts a deploy that has not
// happened. That green badge survives even when the build later FAILS.
const placeholderImagePrefix = "registry.k8s.io/pause"

// RestatePlaceholderPhase rewrites the phase of any app still running the
// placeholder image so it reports what is actually true, and drops its "url" —
// a pause container answers no HTTP request, so the surrogate domain is a
// guaranteed 502 until the real image lands.
//
// The replacement phase comes from the app's latest build (name → status in
// buildStatus): a queued/running build means "Building", a failed one means
// "Failed", anything else means the app was never really deployed. Only
// "Failed" is terminal for the frontend poller, so a page watching a
// still-building app keeps polling instead of settling on a false Ready.
func RestatePlaceholderPhase(apps []models.ResourceSnapshot, buildStatus map[string]string) {
	for i := range apps {
		if len(apps[i].SummaryJSON) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(apps[i].SummaryJSON, &m); err != nil {
			continue
		}
		image, _ := m["image"].(string)
		if !strings.HasPrefix(image, placeholderImagePrefix) {
			continue
		}
		switch buildStatus[apps[i].Name] {
		case "queued", "running":
			apps[i].Phase = "Building"
		case "failed":
			apps[i].Phase = "Failed"
		default:
			apps[i].Phase = "NotDeployed"
		}
		if _, hasURL := m["url"]; hasURL {
			delete(m, "url")
			if b, err := json.Marshal(m); err == nil {
				apps[i].SummaryJSON = b
			}
		}
	}
}

// GitRepoRow is one git_repos row plus its latest build status — the inputs
// SynthesizeGitRepoApps needs to decide whether to surface a NotDeployed
// placeholder app for a repo that has no live snapshot yet.
type GitRepoRow struct {
	ID           uuid.UUID
	Name         string
	Repo         string
	Profile      string
	Replicas     int
	Port         int
	Updated      time.Time
	LatestStatus string
}

// SynthesizeGitRepoApps appends a NotDeployed placeholder app for each git_repos
// row not already represented by a live snapshot (name absent from seen). A repo
// whose latest build was canceled is skipped so a canceled first deploy leaves
// no visible app — the connect+build+deploy flow then reads as atomic. Failed
// builds are kept so the user can still see and retry them. It returns the
// extended app slice and a name→repo map covering every row (used to backfill
// repo_full_name on deployed apps, independent of which placeholders are shown).
func SynthesizeGitRepoApps(apps []models.ResourceSnapshot, rows []GitRepoRow, seen map[string]struct{}, projectID, envID uuid.UUID) ([]models.ResourceSnapshot, map[string]string) {
	repoByName := make(map[string]string, len(rows))
	for _, r := range rows {
		if r.Repo != "" {
			repoByName[r.Name] = r.Repo
		}
		if _, ok := seen[r.Name]; ok {
			continue
		}
		if r.LatestStatus == "canceled" {
			continue
		}
		summary, _ := json.Marshal(map[string]any{
			"image":          r.Repo,
			"profile":        r.Profile,
			"replicas":       r.Replicas,
			"port":           r.Port,
			"repo_full_name": r.Repo,
			"source":         "git",
		})
		envRef := envID
		apps = append(apps, models.ResourceSnapshot{
			ID:            r.ID,
			ProjectID:     projectID,
			EnvironmentID: &envRef,
			Kind:          "App",
			Name:          r.Name,
			Phase:         "NotDeployed",
			SummaryJSON:   summary,
			LastSyncedAt:  r.Updated,
		})
		seen[r.Name] = struct{}{}
	}
	return apps, repoByName
}

// ListApps returns all App resources in a project environment.
//
// Apps come from two sources: live resource_snapshots (deployed workloads) and
// git_repos rows synthesized as NotDeployed placeholders. A placeholder whose
// latest build was canceled is omitted so a canceled first deploy leaves no
// visible app — the connect+build+deploy flow then reads as atomic (either the
// app deploys or nothing lingers). Failed builds are kept so the user can retry.
//
// @ID          listApps
// @Summary     List apps in an environment
// @Description Returns all App resources (Helm or compose) in a project environment, with their live phase/status. Read-only.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Success     200       {object} map[string]interface{} "object with an apps array of ResourceSnapshot"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps [get]
func (h *Handler) ListApps(c *gin.Context) {
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

	_, err = h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, project_id, environment_id, kind, name, phase, summary_json, last_synced_at
		 FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App'
		 ORDER BY name`,
		projectID, envID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query apps")
		return
	}
	defer rows.Close()

	var apps []models.ResourceSnapshot
	for rows.Next() {
		var rs models.ResourceSnapshot
		if err := rows.Scan(
			&rs.ID, &rs.ProjectID, &rs.EnvironmentID, &rs.Kind, &rs.Name,
			&rs.Phase, &rs.SummaryJSON, &rs.LastSyncedAt,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan app")
			return
		}
		apps = append(apps, rs)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading apps")
		return
	}
	if apps == nil {
		apps = []models.ResourceSnapshot{}
	}

	seen := make(map[string]struct{}, len(apps))
	for _, a := range apps {
		seen[a.Name] = struct{}{}
	}
	grows, gerr := h.pool.Query(c.Request.Context(),
		`SELECT gr.id, gr.app_name, gr.repo_full_name,
		        COALESCE(gr.profile, 'small'), COALESCE(gr.replicas, 1), COALESCE(gr.port, 8080),
		        gr.updated_at, COALESCE(lb.status, '')
		 FROM git_repos gr
		 LEFT JOIN LATERAL (
		     SELECT status FROM builds b
		     WHERE b.git_repo_id = gr.id
		     ORDER BY b.created_at DESC
		     LIMIT 1
		 ) lb ON true
		 WHERE gr.project_id = $1 AND gr.environment_id = $2`,
		projectID, envID,
	)
	var gitRows []GitRepoRow
	if gerr == nil {
		defer grows.Close()
		for grows.Next() {
			var r GitRepoRow
			if scanErr := grows.Scan(&r.ID, &r.Name, &r.Repo, &r.Profile, &r.Replicas, &r.Port, &r.Updated, &r.LatestStatus); scanErr != nil {
				continue
			}
			gitRows = append(gitRows, r)
		}
	}
	buildStatus := make(map[string]string, len(gitRows))
	for _, r := range gitRows {
		buildStatus[r.Name] = r.LatestStatus
	}
	apps, repoByName := SynthesizeGitRepoApps(apps, gitRows, seen, projectID, envID)

	FillRepoFullName(apps, repoByName)
	FillEffectiveResources(apps)
	RestatePlaceholderPhase(apps, buildStatus)
	SuppressNonHTTPURL(apps)
	EnrichPreviewURL(apps, envID, h.cfg)

	if ns := h.environmentNamespace(c.Request.Context(), envID); ns != "" {
		if byApp, aerr := h.loadAppAlerts(c.Request.Context(), ns); aerr == nil {
			applyAppAlerts(apps, byApp)
		}
	}

	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })

	c.JSON(http.StatusOK, gin.H{"apps": apps})
}

// jsFrameworks are the detected framework labels whose serve/dev process listens
// on :5173 by default (mirrors the helm common chart's javascript stack and
// renderer.ChartFor). Kept local so the backend module does not depend on the
// gitops-agent renderer package.
var jsFrameworks = map[string]bool{
	"javascript": true, "web": true, "nextjs": true, "nuxt": true,
	"sveltekit": true, "react": true, "nestjs": true, "express": true,
	"fastify": true, "remix": true, "vite": true, "node": true,
}

// defaultPortForFramework returns the servicePort to assume when a create request
// omits one: 5173 for javascript apps, 8080 otherwise.
func defaultPortForFramework(framework string) int {
	if jsFrameworks[framework] {
		return 5173
	}
	return 8080
}

// datastorePorts are well-known TCP ports that speak a binary protocol, not
// HTTP: redis 6379, postgres 5432/5433, mysql/mariadb 3306, mssql 1433,
// mongodb 27017, rabbitmq amqp 5672, kafka 9092, memcached 11211, zookeeper
// 2181, cockroachdb 26257. An app listening only on one of these cannot answer
// an HTTP request, so auto-attaching a default surrogate hostname produces a
// guaranteed 502 and an attack-log-noise magnet (see top-decker redis:latest).
// Deploys on these ports skip the auto domain; the user gets an internal
// service instead.
var datastorePorts = map[int]bool{
	6379:  true,
	5432:  true,
	5433:  true,
	3306:  true,
	1433:  true,
	27017: true,
	5672:  true,
	9092:  true,
	11211: true,
	2181:  true,
	26257: true,
}

// servesHTTP reports whether an app on servicePort should get an auto public
// hostname. Conservative: only ports known to be non-HTTP datastores are
// excluded, so ordinary web apps on any other port keep their default domain.
func servesHTTP(servicePort int) bool {
	return !datastorePorts[servicePort]
}

type createAppRequest struct {
	Name         string        `json:"name"`
	Image        string        `json:"image"`
	Framework    string        `json:"framework"`
	Port         int           `json:"port"`
	Replicas     int           `json:"replicas"`
	Profile      string        `json:"profile"`
	WorkloadType string        `json:"workload_type"`
	Volume       *appVolumeReq `json:"volume,omitempty"`
	Worker       bool          `json:"worker"`
}

// appVolumeReq is the wire form of a persistent data directory request. Empty
// StorageClass defaults to defaultVolumeStorageClass (a Retain longhorn class).
type appVolumeReq struct {
	Path         string `json:"path"`
	Size         string `json:"size"`
	StorageClass string `json:"storage_class"`
}

// defaultVolumeStorageClass is a ReadWriteMany-capable Longhorn class with
// reclaimPolicy=Retain, so deleting the PVC does not immediately destroy data.
//
// It defaults to the 1-replica longhorn-dev class. beget-prod runs three Longhorn
// nodes with strict node anti-affinity under chronic disk pressure that keeps two
// or more nodes below the schedulable floor at once, so any multi-replica volume
// cannot place its extra replicas: the Longhorn Volume reports Scheduled=False
// (ReplicaSchedulingFailure) and refuses to attach, wedging the pod in
// ContainerCreating for the lifetime of the pressure. A single replica needs only
// one schedulable node and attaches cleanly (and halves dev-tier disk use). The
// tradeoff is no redundancy -- a node rotation/fault loses the volume -- accepted
// for the dev tier; the 3-replica classes stay allowed below for stateful/prod
// data and for resizing existing volumes.
const defaultVolumeStorageClass = "longhorn-dev"

var volumeSizeRe = regexp.MustCompile(`^[1-9][0-9]*(Mi|Gi|Ti)$`)

// absoluteMaxVolumeSize is a hard safety ceiling on a per-app persistent
// volume, independent of billing plan. The plan-aware cap is enforced
// separately (see storageCapBytes in billing.go); this constant only stops a
// fat-finger request larger than the biggest plan's quota from ever reaching
// the Argo apply step, where an oversized PVC would otherwise fail admission
// (silent, retry-looping) instead of erroring clearly here.
const absoluteMaxVolumeSize = "100Gi"

// allowedVolumeStorageClasses guards against pointing app data at ephemeral
// (reclaimPolicy=Delete) classes. All entries are Retain on beget-prod.
// longhorn-dev is the 1-replica default (see defaultVolumeStorageClass); the
// 3-replica classes remain selectable for stateful/prod data and existing volumes.
var allowedVolumeStorageClasses = map[string]bool{
	"longhorn-dev":           true,
	"longhorn-prod":          true,
	"longhorn-stateful-prod": true,
}

// validateAppVolume normalises and validates a persistent-directory request,
// returning the typed model payload. A nil request yields a nil volume (no PVC).
func validateAppVolume(v *appVolumeReq) (*models.AppVolume, error) {
	if v == nil {
		return nil, nil
	}
	if !strings.HasPrefix(v.Path, "/") || strings.Contains(v.Path, "..") || len(v.Path) < 2 {
		return nil, fmt.Errorf("volume path must be an absolute path without '..'")
	}
	if !volumeSizeRe.MatchString(v.Size) {
		return nil, fmt.Errorf("volume size must be a quantity like 1Gi, 512Mi or 2Ti")
	}
	if quantityBytes(v.Size) > quantityBytes(absoluteMaxVolumeSize) {
		return nil, fmt.Errorf("volume size must not exceed %s per app", absoluteMaxVolumeSize)
	}
	sc := v.StorageClass
	if sc == "" {
		sc = defaultVolumeStorageClass
	}
	if !allowedVolumeStorageClasses[sc] {
		return nil, fmt.Errorf("storage_class must be one of: longhorn-dev, longhorn-prod, longhorn-stateful-prod")
	}
	return &models.AppVolume{Path: v.Path, Size: v.Size, StorageClass: sc}, nil
}

// CreateApp enqueues an operation to provision a new App CRD.
//
// @ID          createApp
// @Summary     Deploy a new app
// @Description Provisions a new app in an environment. For Kubernetes (Helm) environments image is required and port/replicas/profile apply; for VM (compose) environments the app deploys as a Docker Compose stack onto the environment's app server, which must already be Ready. Asynchronous: returns 202 with an operation; poll the operation until terminal.
// @Tags        app
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string           true "Project UUID"
// @Param       envId     path     string           true "Environment UUID"
// @Param       body      body     createAppRequest true "App specification"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps [post]
func (h *Handler) CreateApp(c *gin.Context) {
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

	var req createAppRequest

	rejectCreate := func(status int, reason, msg string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "CreateApp",
			ResourceKind:  "App",
			ResourceName:  req.Name,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status},
		})
		respondError(c, status, msg)
	}

	if orgID, orgErr := h.projectOrg(c.Request.Context(), projectID); orgErr == nil {
		if qErr := h.checkQuota(c.Request.Context(), orgID, "apps"); qErr != nil {
			if qe, ok := qErr.(*quotaExceededError); ok {
				h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
					ProjectID:     projectID,
					EnvironmentID: envID,
					Action:        "CreateApp",
					ResourceKind:  "App",
					Outcome:       auditOutcomeFailure,
					Metadata:      map[string]any{"reason": "quota_exceeded", "resource": qe.Resource, "limit": qe.Limit},
				})
				respondQuotaExceeded(c, qe.Resource, qe.Limit)
				return
			}
		}
	}

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
	isCompose := runtime == models.EnvironmentRuntimeVM

	// For VM environments, the app deploys as a Docker Compose stack onto the
	// environment's AppServer. That server must exist and be Ready.
	var appServerName string
	if isCompose {
		var status string
		err = h.pool.QueryRow(c.Request.Context(),
			`SELECT s.name, s.status
			 FROM environments e JOIN app_servers s ON s.id = e.app_server_id
			 WHERE e.id = $1 AND e.project_id = $2`,
			envID, projectID,
		).Scan(&appServerName, &status)
		if err == pgx.ErrNoRows {
			rejectCreate(http.StatusConflict, "no_appserver", "this VM environment has no AppServer attached; create or attach one first")
			return
		}
		if err != nil {
			respondError(c, http.StatusInternalServerError, "failed to load environment AppServer")
			return
		}
		if status != string(models.AppServerStatusReady) {
			rejectCreate(http.StatusConflict, "appserver_not_ready", "the environment's AppServer is not Ready yet")
			return
		}
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		rejectCreate(http.StatusBadRequest, "malformed_body", err.Error())
		return
	}

	// Validate name (common to both runtimes).
	if req.Name == "" {
		rejectCreate(http.StatusBadRequest, "name_required", "name is required")
		return
	}
	if err := validateKubeName(req.Name); err != nil {
		rejectCreate(http.StatusBadRequest, "invalid_name", err.Error())
		return
	}

	if !isCompose {
		// Helm app validation + defaults.
		if req.Port == 0 {
			req.Port = defaultPortForFramework(req.Framework)
		}
		if req.Replicas == 0 && !claims.IsPlatformAdmin() {
			req.Replicas = 1
		}
		if req.Profile == "" {
			req.Profile = "small"
		}
		if req.Image == "" {
			rejectCreate(http.StatusBadRequest, "image_required", "image is required")
			return
		}
		if err := ValidateImage(req.Image); err != nil {
			rejectCreate(http.StatusBadRequest, "invalid_image", err.Error())
			return
		}
		if req.Port < 1 || req.Port > 65535 {
			rejectCreate(http.StatusBadRequest, "invalid_port", "port must be between 1 and 65535")
			return
		}
		minReplicas := 1
		if claims.IsPlatformAdmin() {
			minReplicas = 0
		}
		if req.Replicas < minReplicas || req.Replicas > 10 {
			rejectCreate(http.StatusBadRequest, "invalid_replicas", fmt.Sprintf("replicas must be between %d and 10", minReplicas))
			return
		}
		validProfiles := map[string]bool{"small": true, "medium": true, "large": true}
		if !validProfiles[req.Profile] {
			rejectCreate(http.StatusBadRequest, "invalid_profile", "profile must be one of: small, medium, large")
			return
		}
		validWorkloadTypes := map[string]bool{"": true, "Deployment": true, "StatefulSet": true}
		if !validWorkloadTypes[req.WorkloadType] {
			rejectCreate(http.StatusBadRequest, "invalid_workload_type", "workload_type must be one of: Deployment, StatefulSet")
			return
		}
	} else {
		if req.WorkloadType != "" {
			rejectCreate(http.StatusBadRequest, "workload_type_not_supported", "workload_type is only supported for Kubernetes apps")
			return
		}
		if req.Worker {
			rejectCreate(http.StatusBadRequest, "worker_not_supported", "worker is only supported for Kubernetes apps")
			return
		}
	}

	appVolume, err := validateAppVolume(req.Volume)
	if err != nil {
		rejectCreate(http.StatusBadRequest, "invalid_volume", err.Error())
		return
	}
	if appVolume != nil && isCompose {
		rejectCreate(http.StatusBadRequest, "storage_not_supported", "persistent storage is only supported for Kubernetes apps")
		return
	}
	if appVolume != nil {
		if orgID, orgErr := h.projectOrg(c.Request.Context(), projectID); orgErr == nil {
			capBytes, limitGB, capErr := h.storageCapBytes(c.Request.Context(), orgID)
			if capErr != nil {
				respondError(c, http.StatusInternalServerError, "failed to resolve storage quota")
				return
			}
			if capBytes > 0 && quantityBytes(appVolume.Size) > capBytes {
				h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
					ProjectID:     projectID,
					EnvironmentID: envID,
					Action:        "CreateApp",
					ResourceKind:  "App",
					ResourceName:  req.Name,
					Outcome:       auditOutcomeFailure,
					Metadata:      map[string]any{"reason": "storage_quota_exceeded", "limit_gb": limitGB},
				})
				respondQuotaExceeded(c, "storage_gb", limitGB)
				return
			}
		}
	}

	// Uniqueness is scoped to THIS project+environment, not the whole Argo instance.
	// New apps get a project-scoped ArgoCD Application name (App CR spec.argoName =
	// "<app>-<env>-<projhash>", consumed by the tenant-apps ApplicationSet), so the
	// same app name in the same env under two different projects no longer collides
	// into one Application. k8s namespaces are already per project+env, so nothing
	// else clashes either. Guarding globally would be a needless cross-tenant name
	// grab (the S3-bucket-global-namespace surprise). See project_appset_name_collision.
	// PRECONDITION: the argo-infra tenant-apps ApplicationSet must consume
	// spec.argoName before this relaxation ships, else a cross-project duplicate
	// still wedges Argo on the bare name.
	var exists int
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT 1
		 FROM resource_snapshots rs
		 WHERE rs.kind = 'App' AND rs.name = $1
		   AND rs.project_id = $2 AND rs.environment_id = $3
		 LIMIT 1`,
		req.Name, projectID, envID,
	).Scan(&exists)
	if err == nil {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "CreateApp",
			ResourceKind:  "App",
			ResourceName:  req.Name,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": "name_taken"},
		})
		respondError(c, http.StatusConflict, fmt.Sprintf(
			"the app name %q is already taken in this project's environment; choose another name",
			req.Name))
		return
	} else if err != pgx.ErrNoRows {
		respondError(c, http.StatusInternalServerError, "failed to check name uniqueness")
		return
	}

	var defaultHostname string
	if !isCompose && !req.Worker && servesHTTP(req.Port) && h.cfg.DefaultDomainEnabled && h.cfg.DefaultDomainBase != "" {
		if suffix, sErr := randomHostSuffix(); sErr == nil {
			defaultHostname = buildDefaultHostname(h.cfg.DefaultDomainBase, req.Name, suffix)
		}
	}

	// Marshal payload
	payload := models.CreateAppPayload{
		Name:            req.Name,
		Image:           req.Image,
		Framework:       req.Framework,
		Port:            req.Port,
		Replicas:        req.Replicas,
		Profile:         req.Profile,
		WorkloadType:    req.WorkloadType,
		Volume:          appVolume,
		AppServerName:   appServerName,
		DefaultHostname: defaultHostname,
		Worker:          req.Worker,
	}
	payloadBytes, err := json.Marshal(payload)
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

	var op models.Operation
	row := tx.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'CreateApp', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, req.Name, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	optimisticSummary := map[string]any{
		"profile":  req.Profile,
		"replicas": req.Replicas,
		"port":     req.Port,
	}
	if req.Worker {
		optimisticSummary["worker"] = true
	}
	if err = seedOptimisticSnapshot(c.Request.Context(), tx, projectID, envID, "App", req.Name, optimisticSummary); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	if defaultHostname != "" {
		if _, err = tx.Exec(c.Request.Context(),
			`INSERT INTO domain_hostnames (authorization_id, environment_id, app_name, hostname, record_type, status, cert_status, operation_id, managed)
			 VALUES (NULL, $1, $2, $3, 'CNAME', 'pending', 'pending', $4, true)
			 ON CONFLICT (hostname) DO NOTHING`,
			envID, req.Name, defaultHostname, op.ID,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to record default hostname")
			return
		}
	}

	if err = tx.Commit(c.Request.Context()); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	// Insert AuditEvent (best-effort)
	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "CreateApp",
		ResourceKind:  "App",
		ResourceName:  req.Name,
		Metadata:      payload,
	})
	h.notifyAuditEvent(claims, projectID, "CreateApp", req.Name)

	c.JSON(http.StatusAccepted, gin.H{
		"operation":        op,
		"default_hostname": defaultHostname,
		"message":          "App creation queued",
	})
}

type updateAppImageRequest struct {
	Image string `json:"image"`
}

// UpdateAppImage enqueues an operation to deploy a new image version for an App.
//
// @ID          updateAppImage
// @Summary     Deploy a new image version for an app
// @Description Rolls an existing app to a new container image. Asynchronous: returns 202 with an operation; poll the operation until terminal.
// @Tags        app
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                true "Project UUID"
// @Param       envId     path     string                true "Environment UUID"
// @Param       appName   path     string                true "App name"
// @Param       body      body     updateAppImageRequest true "New image reference"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/image [patch]
func (h *Handler) UpdateAppImage(c *gin.Context) {
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

	var req updateAppImageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	if req.Image == "" {
		respondError(c, http.StatusBadRequest, "image is required")
		return
	}
	if err := ValidateImage(req.Image); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	// Verify app exists
	var count int
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&count)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check app existence")
		return
	}
	if count == 0 {
		respondNotFound(c)
		return
	}

	// Marshal payload
	payload := models.DeployImageVersionPayload{
		AppName: appName,
		Image:   req.Image,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to marshal payload")
		return
	}

	// Insert Operation
	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'DeployImageVersion', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, appName, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "DeployImageVersion",
		ResourceKind:  "App",
		ResourceName:  appName,
		Metadata:      payload,
	})

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"message":   "Image update queued",
	})
}

type updateAppProfileRequest struct {
	Profile string `json:"profile"`
}

var validAppProfiles = map[string]bool{"small": true, "medium": true, "large": true}

// UpdateAppProfile resizes an app to one of the legacy small/medium/large
// sizes. It writes both the profile name and the explicit envelope that name
// resolves to into resource_snapshots.summary_json, then enqueues the same
// DeployImageVersion operation UpdateAppImage uses, keeping the current image,
// so gitops-agent re-renders the workload chart with the new requests/limits.
//
// Writing the envelope and not just the name is what keeps this endpoint from
// becoming a silent no-op: the renderer prefers an explicit envelope, so on an
// app the autoscaler has already grown, a bare profile name would change
// nothing. This is the operator's way to put such an app back down; the console
// no longer offers sizes to users, who get autoscaling instead.
//
// @ID          updateAppProfile
// @Summary     Resize an app's CPU/memory profile
// @Description Changes an app's resource profile (small, medium, or large) and redeploys it with the new CPU/memory limits. Asynchronous: returns 202 with an operation; poll the operation until terminal.
// @Tags        app
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                  true "Project UUID"
// @Param       envId     path     string                  true "Environment UUID"
// @Param       appName   path     string                  true "App name"
// @Param       body      body     updateAppProfileRequest true "New resource profile"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/profile [patch]
func (h *Handler) UpdateAppProfile(c *gin.Context) {
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
	if !h.requireK8sRuntime(c, projectID, envID) {
		return
	}

	var req updateAppProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if !validAppProfiles[req.Profile] {
		respondError(c, http.StatusBadRequest, "profile must be one of: small, medium, large")
		return
	}

	tx, err := h.pool.Begin(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	var summaryRaw []byte
	err = tx.QueryRow(c.Request.Context(),
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3
		 FOR UPDATE`,
		projectID, envID, appName,
	).Scan(&summaryRaw)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load app")
		return
	}

	var cur map[string]any
	_ = json.Unmarshal(summaryRaw, &cur)
	if cur == nil {
		cur = map[string]any{}
	}
	image, _ := cur["image"].(string)
	if image == "" {
		respondError(c, http.StatusBadRequest, "app has no deployed image to resize")
		return
	}

	cur["profile"] = req.Profile
	cur["resources"] = autoscaleProfileRequirements[req.Profile].snapshot()
	updatedJSON, err := json.Marshal(cur)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to marshal snapshot")
		return
	}
	if _, err = tx.Exec(c.Request.Context(),
		`UPDATE resource_snapshots SET summary_json = $1
		 WHERE project_id = $2 AND environment_id = $3 AND kind = 'App' AND name = $4`,
		updatedJSON, projectID, envID, appName,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to update app profile")
		return
	}

	if _, err = tx.Exec(c.Request.Context(),
		`UPDATE git_repos SET profile = $1
		 WHERE project_id = $2 AND environment_id = $3 AND app_name = $4`,
		req.Profile, projectID, envID, appName,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to update app profile")
		return
	}

	payload := models.DeployImageVersionPayload{AppName: appName, Image: image}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to marshal payload")
		return
	}

	var op models.Operation
	row := tx.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'DeployImageVersion', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, appName, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	if err = tx.Commit(c.Request.Context()); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "UpdateAppProfile",
		ResourceKind:  "App",
		ResourceName:  appName,
		Metadata:      payload,
	})

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"message":   "Profile update queued",
	})
}

// quantityBytes converts a restricted k8s quantity (Mi/Gi/Ti, validated by
// volumeSizeRe) to a byte count for grow-only comparisons. It returns 0 for any
// value that does not match the expected shape.
func quantityBytes(q string) int64 {
	if !volumeSizeRe.MatchString(q) {
		return 0
	}
	unit := q[len(q)-2:]
	num, err := strconv.ParseInt(q[:len(q)-2], 10, 64)
	if err != nil {
		return 0
	}
	switch unit {
	case "Mi":
		return num * 1024 * 1024
	case "Gi":
		return num * 1024 * 1024 * 1024
	case "Ti":
		return num * 1024 * 1024 * 1024 * 1024
	}
	return 0
}

// UpdateAppStorage attaches or resizes the persistent data directory of a Helm
// app. A PersistentVolumeClaim is immutable once created, so an existing volume
// may only grow and may not change its storage class; the mount path may change.
//
// @ID          updateAppStorage
// @Summary     Attach or resize app persistent storage
// @Description Attaches a ReadWriteMany persistent data directory to a Kubernetes app, or resizes an existing one (grow-only; storage class is fixed once created). Asynchronous: returns 202 with an operation; poll it until terminal.
// @Tags        app
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string       true "Project UUID"
// @Param       envId     path     string       true "Environment UUID"
// @Param       appName   path     string       true "App name"
// @Param       body      body     appVolumeReq true "Persistent storage specification"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     500       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/storage [put]
func (h *Handler) UpdateAppStorage(c *gin.Context) {
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

	var req appVolumeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	vol, err := validateAppVolume(&req)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	var summaryRaw []byte
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&summaryRaw)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load app")
		return
	}

	var cur struct {
		Volume *models.AppVolume `json:"volume"`
	}
	_ = json.Unmarshal(summaryRaw, &cur)
	if cur.Volume != nil {
		if cur.Volume.StorageClass != "" && cur.Volume.StorageClass != vol.StorageClass {
			respondError(c, http.StatusBadRequest, "storage class cannot be changed after the volume is created")
			return
		}
		if quantityBytes(vol.Size) < quantityBytes(cur.Volume.Size) {
			respondError(c, http.StatusBadRequest, "storage can only be expanded, not shrunk")
			return
		}
	}

	if orgID, orgErr := h.projectOrg(c.Request.Context(), projectID); orgErr == nil {
		capBytes, limitGB, capErr := h.storageCapBytes(c.Request.Context(), orgID)
		if capErr != nil {
			respondError(c, http.StatusInternalServerError, "failed to resolve storage quota")
			return
		}
		if capBytes > 0 {
			allowedBytes := capBytes
			if cur.Volume != nil && quantityBytes(cur.Volume.Size) > allowedBytes {
				allowedBytes = quantityBytes(cur.Volume.Size)
			}
			if quantityBytes(vol.Size) > allowedBytes {
				respondQuotaExceeded(c, "storage_gb", limitGB)
				return
			}
		}
	}

	payload := models.UpdateAppStoragePayload{AppName: appName, Volume: *vol}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to marshal payload")
		return
	}

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'UpdateAppStorage', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, appName, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "UpdateAppStorage",
		ResourceKind:  "App",
		ResourceName:  appName,
		Metadata:      payload,
	})

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"message":   "Storage update queued",
	})
}

// RollbackApp enqueues a RollbackStack operation that reverts a compose app's
// compose.yaml to its previous committed version and redeploys — the VM-runtime
// "Rollback" action (ADR-013 §8.3). Git-native + data-safe (the external PG
// volume pin is in every version). No body.
//
// @ID          rollbackApp
// @Summary     Roll a compose app back to its previous version
// @Description Reverts the app's compose.yaml to the previous committed version and redeploys the stack. Compose (VM) apps only. Asynchronous: returns 202 with an operation; poll until terminal.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/rollback [post]
func (h *Handler) RollbackApp(c *gin.Context) {
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

	var count int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&count); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check app existence")
		return
	}
	if count == 0 {
		respondNotFound(c)
		return
	}
	if !h.requireVMRuntime(c, projectID, envID) {
		return
	}

	payloadBytes, _ := json.Marshal(map[string]string{"app_name": appName})

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'RollbackStack', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, appName, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "RollbackStack",
		ResourceKind:  "App",
		ResourceName:  appName,
		Metadata:      map[string]string{"app_name": appName},
	})

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "rollback queued"})
}

// AdoptApp enqueues an AdoptComposeStack operation that splits an existing
// single compose App into N first-class per-service Applications, preserving the
// live stack byte-faithfully (verbatim service blocks + the external-volume name
// mapping). Reusable "adopt an existing compose" action; the postgres external
// volume survives the stack swap so prod data is preserved (brief cutover
// outage). Compose (VM) apps only. No body — the path app IS the source.
//
// @ID          adoptApp
// @Summary     Adopt a compose app into per-service Applications
// @Description Splits an existing single compose App into one first-class Application per service, reproducing the live stack (verbatim blocks + preserved external volumes) and redeploying it. Compose (VM) apps only. Asynchronous: returns 202 with an operation; poll until terminal.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "Source app name"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/adopt [post]
func (h *Handler) AdoptApp(c *gin.Context) {
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

	var count int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&count); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check app existence")
		return
	}
	if count == 0 {
		respondNotFound(c)
		return
	}
	if !h.requireVMRuntime(c, projectID, envID) {
		return
	}

	payloadBytes, _ := json.Marshal(map[string]string{"source_app": appName})

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'AdoptComposeStack', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, appName, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "AdoptComposeStack",
		ResourceKind:  "App",
		ResourceName:  appName,
		Metadata:      map[string]string{"source_app": appName},
	})

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "adopt queued"})
}

// RestartApp enqueues a RestartStack operation that recreates a compose app's
// containers from the current git compose (no image pull) — the VM-runtime
// "Restart" action (ADR-013 §8.3). No body.
//
// @ID          restartApp
// @Summary     Restart a compose app
// @Description Recreates the compose app's containers from the current compose.yaml without pulling new images or touching volumes. Compose (VM) apps only. Asynchronous: returns 202 with an operation.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/restart [post]
func (h *Handler) RestartApp(c *gin.Context) {
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

	var count int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&count); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check app existence")
		return
	}
	if count == 0 {
		respondNotFound(c)
		return
	}
	if !h.requireVMRuntime(c, projectID, envID) {
		return
	}

	payloadBytes, _ := json.Marshal(map[string]string{"app_name": appName})

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'RestartStack', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, appName, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "RestartStack",
		ResourceKind:  "App",
		ResourceName:  appName,
		Metadata:      map[string]string{"app_name": appName},
	})

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "restart queued"})
}

type updateComposeConfigRequest struct {
	Image string   `json:"image"`
	Ports []string `json:"ports"`
}

// UpdateComposeConfig patches the desired service spec (image + published ports)
// of a compose (VM) app. It is the compose analogue of the Helm values editor:
// the source of truth is the app's resource_snapshots.desired block, so the
// change is applied by the gitops-agent worker (patch snapshot -> re-render the
// environment's aggregate stack), not by editing a git file directly.
//
// @ID          updateComposeConfig
// @Summary     Update a compose app's service config
// @Description Patches the image and published ports of a compose (VM) app's service in the environment's aggregate stack. VM (compose) apps only. Asynchronous: returns 202 with an operation; poll until terminal.
// @Tags        app
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                     true "Project UUID"
// @Param       envId     path     string                     true "Environment UUID"
// @Param       appName   path     string                     true "App name"
// @Param       body      body     updateComposeConfigRequest true "Compose service config"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/compose-config [patch]
func (h *Handler) UpdateComposeConfig(c *gin.Context) {
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
	if !h.requireVMRuntime(c, projectID, envID) {
		return
	}

	var req updateComposeConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	req.Image = strings.TrimSpace(req.Image)
	if req.Image == "" {
		respondError(c, http.StatusBadRequest, "image is required")
		return
	}

	var count int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&count); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check app existence")
		return
	}
	if count == 0 {
		respondNotFound(c)
		return
	}

	payloadBytes, _ := json.Marshal(models.UpdateComposeConfigPayload{
		AppName: appName,
		Image:   req.Image,
		Ports:   req.Ports,
	})

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'UpdateComposeConfig', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, appName, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "UpdateComposeConfig",
		ResourceKind:  "App",
		ResourceName:  appName,
		Metadata:      map[string]string{"app_name": appName, "image": req.Image},
	})

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "compose config update queued"})
}

type updateComposeVolumeRequest struct {
	Volumes []string `json:"volumes"`
}

// UpdateComposeVolume sets the named-volume mounts of a compose (VM) app's
// service. Like the config editor it patches the app's desired block and defers
// to the gitops-agent worker, which re-renders the aggregate stack and
// re-derives the external-volume pins so existing data is preserved.
//
// @ID          updateComposeVolume
// @Summary     Update a compose app's named-volume mounts
// @Description Sets the named-volume mounts (source:target) of a compose (VM) app's service and redeploys the stack. VM (compose) apps only. Asynchronous: returns 202 with an operation; poll until terminal.
// @Tags        app
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                     true "Project UUID"
// @Param       envId     path     string                     true "Environment UUID"
// @Param       appName   path     string                     true "App name"
// @Param       body      body     updateComposeVolumeRequest true "Compose volume mounts"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/compose-volume [put]
func (h *Handler) UpdateComposeVolume(c *gin.Context) {
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
	if !h.requireVMRuntime(c, projectID, envID) {
		return
	}

	var req updateComposeVolumeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	for _, v := range req.Volumes {
		if !strings.Contains(v, ":") || strings.HasPrefix(strings.TrimSpace(v), "/") || strings.HasPrefix(strings.TrimSpace(v), ".") {
			respondError(c, http.StatusBadRequest, "each volume must be a named mount in source:target form (bind mounts are not allowed)")
			return
		}
	}

	var count int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&count); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check app existence")
		return
	}
	if count == 0 {
		respondNotFound(c)
		return
	}

	payloadBytes, _ := json.Marshal(models.UpdateComposeVolumePayload{
		AppName: appName,
		Volumes: req.Volumes,
	})

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'UpdateComposeVolume', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, appName, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "UpdateComposeVolume",
		ResourceKind:  "App",
		ResourceName:  appName,
		Metadata:      map[string]string{"app_name": appName},
	})

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "compose volume update queued"})
}

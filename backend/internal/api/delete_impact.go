package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// ImpactItem is one resource the delete-impact scan found for an app: either a
// console-tracked child resource_snapshot, or a live cluster object with no
// matching snapshot ("cluster-only" — the real blast-radius surprise).
type ImpactItem struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Group  string `json:"group"`
	Source string `json:"source"`
}

// DeleteImpact is the full delete-impact preview for one app.
type DeleteImpact struct {
	App         string       `json:"app"`
	Namespace   string       `json:"namespace"`
	Items       []ImpactItem `json:"items"`
	ClusterOnly int          `json:"cluster_only"`
	ClusterScan bool         `json:"cluster_scan"`
}

// withNonNilItems guarantees Items is a non-nil slice so it JSON-encodes as
// `"items": []` rather than `"items": null`. A nil slice is the default when a
// scan finds nothing, and the console delete modal iterates Items during render:
// a null there throws "null is not an object" and crashes the whole page.
func (d DeleteImpact) withNonNilItems() DeleteImpact {
	if d.Items == nil {
		d.Items = []ImpactItem{}
	}
	return d
}

const (
	impactSourceConsole     = "console"
	impactSourceClusterOnly = "cluster-only"

	impactGroupDomain      = "domain"
	impactGroupDatabase    = "database"
	impactGroupStorage     = "storage"
	impactGroupIngress     = "ingress"
	impactGroupCertificate = "certificate"
	impactGroupOther       = "other"
)

func platformGVR(resource string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "platform.dada-tuda.ru", Version: "v1alpha1", Resource: resource}
}

var (
	ingressGVR           = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}
	pvcGVR               = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumeclaims"}
	certificateGVR       = schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}
	publicApiGVR         = platformGVR("publicapis")
	serviceDatabaseV2GVR = platformGVR("servicedatabasesv2")
	s3BucketGVR          = platformGVR("s3buckets")
)

// clusterScanTarget declares one live GVR to scan for app-owned resources, and
// which impact Group it maps to.
type clusterScanTarget struct {
	gvr   schema.GroupVersionResource
	kind  string
	group string
}

var clusterScanTargets = []clusterScanTarget{
	{ingressGVR, "Ingress", impactGroupIngress},
	{publicApiGVR, "PublicApi", impactGroupDomain},
	{serviceDatabaseV2GVR, "ServiceDatabaseV2", impactGroupDatabase},
	{s3BucketGVR, "S3Bucket", impactGroupStorage},
	{certificateGVR, "Certificate", impactGroupCertificate},
	{pvcGVR, "PersistentVolumeClaim", impactGroupStorage},
}

// snapshotGroup maps a resource_snapshots.kind to the impact Group used in the
// modal's grouping/counts.
func snapshotGroup(kind string) string {
	switch kind {
	case "PublicApi":
		return impactGroupDomain
	case "ServiceDatabaseV2":
		return impactGroupDatabase
	case "S3Bucket":
		return impactGroupStorage
	default:
		return impactGroupOther
	}
}

// deleteImpactScanner reads live cluster objects via the pod's in-cluster
// dynamic client. Off-cluster (local dev, no service-account mount) it is
// disabled: Scan always degrades to an empty result rather than erroring, so
// impact handlers can fall back to console-only impact instead of a 500.
type deleteImpactScanner struct {
	dyn dynamic.Interface
}

// newDeleteImpactScanner builds a scanner backed by the pod's mounted
// service-account credentials, mirroring the rest.InClusterConfig() +
// dynamic.NewForConfig pattern used by cloudtask (kanister.go, counter.go).
// Off-cluster it returns a disabled scanner whose Scan is a no-op.
func newDeleteImpactScanner() *deleteImpactScanner {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return &deleteImpactScanner{}
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return &deleteImpactScanner{}
	}
	return &deleteImpactScanner{dyn: dyn}
}

func (s *deleteImpactScanner) enabled() bool { return s.dyn != nil }

// scanApp lists every clusterScanTarget in namespace and keeps objects that
// belong to appName by an OWNERSHIP signal Argo also prunes on: label
// dada.io/app=appName, OR ArgoCD instance label "<appName>-<envName>" (Argo
// stamps this on every child of an Application), OR object name == appName.
// A bare name prefix ("<appName>-") is deliberately NOT used: it cross-matches
// sibling apps (deleting "profi" would falsely claim "profi-backend" resources).
// List errors (missing CRD, 403 RBAC) are swallowed per-target so one missing
// resource type never blanks the whole scan.
func (s *deleteImpactScanner) scanApp(ctx context.Context, namespace, appName, envName string) []ImpactItem {
	if !s.enabled() {
		return nil
	}
	instance := appName + "-" + envName

	var items []ImpactItem
	for _, target := range clusterScanTargets {
		list, err := s.dyn.Resource(target.gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}
		for i := range list.Items {
			obj := &list.Items[i]
			name := obj.GetName()
			labels := obj.GetLabels()
			owned := labels["dada.io/app"] == appName ||
				labels["argocd.argoproj.io/instance"] == instance ||
				name == appName
			if !owned {
				continue
			}
			items = append(items, ImpactItem{
				Kind:   target.kind,
				Name:   name,
				Group:  target.group,
				Source: impactSourceClusterOnly,
			})
		}
	}
	return items
}

// consoleImpact returns the console-managed child resource_snapshots for an
// app: the exact set doDeleteApp cascades (gitops-agent dbwatcher.go), matched
// by whichever owning-app key the writer stamped.
func (h *Handler) consoleImpact(ctx context.Context, projectID, envID uuid.UUID, appName string) ([]ImpactItem, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT kind, name FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind <> 'App'
		   AND (
		        summary_json->>'app_ref'             = $3
		     OR summary_json->>'attached_app'        = $3
		     OR summary_json->>'app_name'            = $3
		     OR summary_json->'spec'->>'appRef'       = $3
		     OR summary_json->'spec'->>'attachedApp'  = $3
		     OR summary_json->'spec'->>'serviceName'  = $3
		   )`,
		projectID, envID, appName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ImpactItem
	for rows.Next() {
		var kind, name string
		if err := rows.Scan(&kind, &name); err != nil {
			return nil, err
		}
		items = append(items, ImpactItem{
			Kind:   kind,
			Name:   name,
			Group:  snapshotGroup(kind),
			Source: impactSourceConsole,
		})
	}
	return items, rows.Err()
}

// appDeleteImpact merges console-managed and cluster-truth impact for a single
// app, deduped by kind+name. Never fails on the cluster half: a disabled or
// 403ing scanner just yields ClusterScan=false / no cluster-only rows.
func (h *Handler) appDeleteImpact(ctx context.Context, projectID, envID uuid.UUID, namespace, appName, envName string) (DeleteImpact, error) {
	console, err := h.consoleImpact(ctx, projectID, envID, appName)
	if err != nil {
		return DeleteImpact{}, fmt.Errorf("console impact: %w", err)
	}

	seen := make(map[string]bool, len(console))
	for _, it := range console {
		seen[it.Kind+"|"+it.Name] = true
	}

	impact := DeleteImpact{App: appName, Namespace: namespace, Items: console}

	scanner := newDeleteImpactScanner()
	impact.ClusterScan = scanner.enabled()
	if scanner.enabled() {
		for _, it := range scanner.scanApp(ctx, namespace, appName, envName) {
			key := it.Kind + "|" + it.Name
			if seen[key] {
				continue
			}
			seen[key] = true
			impact.Items = append(impact.Items, it)
			impact.ClusterOnly++
		}
	}
	return impact.withNonNilItems(), nil
}

// DeleteAppImpact previews the blast radius of deleting an app: console-tracked
// child resources plus a live cluster scan for anything the console never
// recorded (e.g. a hand-created PublicApi/Ingress). cluster_only items are the
// danger set the UI must red-flag before allowing delete.
//
// @ID          deleteAppImpact
// @Summary     Preview the blast radius of deleting an app
// @Description Merges console-tracked child resources with a live cluster scan (Ingress, PublicApi, ServiceDatabaseV2, S3Bucket, Certificate, PVC) so the UI can show the real deletion blast radius, including resources the console never recorded.
// @Tags        apps
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} DeleteImpact
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/delete-impact [get]
func (h *Handler) DeleteAppImpact(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	if _, err := h.requireMember(c, claims.UserID, projectID); err != nil {
		return
	}
	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return
	} else if !ok {
		respondNotFound(c)
		return
	}
	appName := c.Param("appName")

	var namespace, envName string
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT namespace, name FROM environments WHERE id = $1`, envID,
	).Scan(&namespace, &envName); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to look up environment")
		return
	}

	impact, err := h.appDeleteImpact(c.Request.Context(), projectID, envID, namespace, appName, envName)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to compute delete impact")
		return
	}
	c.JSON(http.StatusOK, impact)
}

// DeleteApp enqueues an async DeleteApp operation. The worker's existing
// doDeleteApp does the actual git removal + snapshot cascade; see
// gitops-agent/internal/worker/dbwatcher.go.
//
// The existence gate accepts EITHER an App snapshot OR a git_repos row: a
// deleted app whose repo link survived resurfaces in ListApps as a NotDeployed
// placeholder (SynthesizeGitRepoApps), and that phantom must be deletable too —
// otherwise the UI delete dialog dead-ends on 404. doDeleteApp is a safe no-op
// on git when the files are already gone (RemoveAndPush skips missing paths).
//
// @ID          deleteApp
// @Summary     Delete an app
// @Description Destructive, asynchronous: enqueues a DeleteApp operation that removes the app's git folder (app.yaml/values.yaml/resources.values.yaml) and every child resource it owns. Call the delete-impact endpoint first to preview the blast radius. Returns 202 with an operation; poll it until terminal.
// @Tags        apps
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName} [delete]
func (h *Handler) DeleteApp(c *gin.Context) {
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
	reject := func(status int, reason string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "DeleteApp",
			ResourceKind:  "App",
			ResourceName:  appName,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status},
		})
	}
	rejectErr := func(status int, reason, msg string) {
		reject(status, reason)
		respondError(c, status, msg)
	}

	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		reject(c.Writer.Status(), "not_a_writer")
		return
	}
	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		rejectErr(http.StatusInternalServerError, "environment_check_failed", "failed to verify environment")
		return
	} else if !ok {
		reject(http.StatusNotFound, "environment_not_in_project")
		respondNotFound(c)
		return
	}

	var exists bool
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM resource_snapshots WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3)
		     OR EXISTS(SELECT 1 FROM git_repos WHERE project_id = $1 AND environment_id = $2 AND app_name = $3)`,
		projectID, envID, appName,
	).Scan(&exists); err != nil {
		rejectErr(http.StatusInternalServerError, "app_lookup_failed", "failed to look up app")
		return
	}
	if !exists {
		reject(http.StatusNotFound, "app_not_found")
		respondNotFound(c)
		return
	}

	payload := models.DeleteAppPayload{Name: appName}
	payloadBytes, _ := json.Marshal(payload)

	tx, err := h.pool.Begin(c.Request.Context())
	if err != nil {
		rejectErr(http.StatusInternalServerError, "operation_insert_failed", "failed to create operation")
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	var op models.Operation
	row := tx.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'DeleteApp', 'App', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, appName, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		rejectErr(http.StatusInternalServerError, "operation_insert_failed", "failed to create operation")
		return
	}

	if err := demoteAppHostnames(c.Request.Context(), tx, envID, appName); err != nil {
		rejectErr(http.StatusInternalServerError, "hostname_demote_failed", "failed to demote app domains")
		return
	}

	if err := tx.Commit(c.Request.Context()); err != nil {
		rejectErr(http.StatusInternalServerError, "operation_insert_failed", "failed to create operation")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "DeleteApp",
		ResourceKind:  "App",
		ResourceName:  appName,
		Outcome:       auditOutcomeSuccess,
	})
	h.notifyAuditEvent(claims, projectID, "DeleteApp", appName)

	_, _ = h.pool.Exec(c.Request.Context(),
		`UPDATE app_deploy_hooks SET revoked_at = now()
		 WHERE project_id = $1 AND environment_id = $2 AND app_name = $3 AND revoked_at IS NULL`,
		projectID, envID, appName,
	)

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "App deletion queued"})
}

// demoteAppHostnames runs, in the caller's DeleteApp transaction, against
// every domain_hostnames row still bound to (environmentID, appName) in
// status 'active' or 'pending'. It flips them to 'failed' with
// status_reason=hostnameReasonAppDeleted instead of deleting them: the row
// carries a verified custom-domain authorization link (or a default-domain
// assignment) that took a real DNS/TXT proof to earn, and DeleteApp itself
// never touched domain_hostnames before this, leaving a hostname pointed at
// an app the gitops path had already removed with no signal and no healing
// path (see project memory project_deleteapp_orphans_domain_row_under_live_app.md).
// Landing the row in 'failed' hands it to ReattachOrphanedHostnames, the
// reconciler already proven to re-drive a failed row once a live App
// snapshot with the same name reappears.
//
// reattach_count is reset to 0 so a row that had already exhausted
// hostnameReattachMaxAttempts in a previous life of the app is not
// permanently stuck once the app comes back under the same name, and
// updated_at is stamped to now() so the hostnameReattachCooldown window
// starts fresh from this delete rather than inheriting a stale clock left
// over from before it. operation_id is cleared since the operation it
// pointed at belonged to that previous life.
func demoteAppHostnames(ctx context.Context, tx pgx.Tx, environmentID uuid.UUID, appName string) error {
	_, err := tx.Exec(ctx,
		`UPDATE domain_hostnames
		    SET status = 'failed', cert_status = 'failed', status_reason = $3,
		        reattach_count = 0, operation_id = NULL, updated_at = now()
		  WHERE environment_id = $1 AND app_name = $2 AND status IN ('active', 'pending')`,
		environmentID, appName, hostnameReasonAppDeleted,
	)
	return err
}

// DeleteProjectImpact previews the blast radius of deleting an entire project:
// the aggregate app-delete impact over every environment/app in the project.
//
// @ID          deleteProjectImpact
// @Summary     Preview the blast radius of deleting a project
// @Description Aggregates the per-app delete-impact scan (console-tracked resources + live cluster scan) over every environment and app in the project.
// @Tags        projects
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/delete-impact [get]
func (h *Handler) DeleteProjectImpact(c *gin.Context) {
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
	if _, err := h.requireMember(c, claims.UserID, projectID); err != nil {
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, name, namespace FROM environments WHERE project_id = $1 ORDER BY name`, projectID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list environments")
		return
	}
	type envRow struct {
		id        uuid.UUID
		name      string
		namespace string
	}
	var envs []envRow
	for rows.Next() {
		var e envRow
		if err := rows.Scan(&e.id, &e.name, &e.namespace); err != nil {
			rows.Close()
			respondError(c, http.StatusInternalServerError, "failed to list environments")
			return
		}
		envs = append(envs, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to list environments")
		return
	}

	var apps []DeleteImpact
	clusterScan := false
	for _, env := range envs {
		appNames, err := h.listEnvAppNames(c.Request.Context(), projectID, env.id)
		if err != nil {
			respondError(c, http.StatusInternalServerError, "failed to list apps")
			return
		}
		for _, appName := range appNames {
			impact, err := h.appDeleteImpact(c.Request.Context(), projectID, env.id, env.namespace, appName, env.name)
			if err != nil {
				respondError(c, http.StatusInternalServerError, "failed to compute delete impact")
				return
			}
			if impact.ClusterScan {
				clusterScan = true
			}
			apps = append(apps, impact)
		}
	}

	totalClusterOnly := 0
	for _, a := range apps {
		totalClusterOnly += a.ClusterOnly
	}

	c.JSON(http.StatusOK, gin.H{
		"project":      projectID,
		"apps":         apps,
		"cluster_only": totalClusterOnly,
		"cluster_scan": clusterScan,
	})
}

// listEnvAppNames returns the app names present in an environment (the App
// resource_snapshots rows), mirroring gitops-agent's listEnvApps.
func (h *Handler) listEnvAppNames(ctx context.Context, projectID, envID uuid.UUID) ([]string, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT name FROM resource_snapshots WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' ORDER BY name`,
		projectID, envID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// DeleteProject enqueues an async DeleteProject operation. The gitops-agent
// worker's doDeleteProject removes the project's whole git tree in one commit
// and wipes its DB rows (FK-safe order); see
// gitops-agent/internal/worker/dbwatcher.go. Namespace teardown and Keycloak
// cleanup are deliberately out of scope for MVP.
//
// @ID          deleteProject
// @Summary     Delete a project
// @Description Destructive, asynchronous: enqueues a DeleteProject operation that removes the project's entire git tree (every environment/app/resource) in one commit and wipes the project's DB rows. Call the project delete-impact endpoint first to preview the blast radius. Returns 202 with an operation; poll it until terminal.
// @Tags        projects
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId} [delete]
func (h *Handler) DeleteProject(c *gin.Context) {
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

	slug := ""
	audit := func(opID uuid.UUID, outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:    projectID,
			OperationID:  opID,
			Action:       "DeleteProject",
			ResourceKind: "Project",
			ResourceName: slug,
			Outcome:      outcome,
			Metadata:     meta,
		})
	}
	rejectErr := func(status int, reason, msg string) {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": reason, "status": status})
		respondError(c, status, msg)
	}

	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "not_a_member", "status": http.StatusNotFound})
		respondNotFound(c)
		return
	}
	if err != nil {
		rejectErr(http.StatusInternalServerError, "membership_check_failed", "failed to check project membership")
		return
	}
	if !canWrite(role) {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "read_only_role", "status": http.StatusForbidden})
		respondForbidden(c)
		return
	}

	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT name FROM projects WHERE id = $1`, projectID,
	).Scan(&slug); err == pgx.ErrNoRows {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "project_not_found", "status": http.StatusNotFound})
		respondNotFound(c)
		return
	} else if err != nil {
		rejectErr(http.StatusInternalServerError, "project_lookup_failed", "failed to look up project")
		return
	}

	payload := models.DeleteProjectPayload{}
	payloadBytes, _ := json.Marshal(payload)

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, 'DeleteProject', 'Project', $3, 'Created', $4)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, slug, payloadBytes,
	)
	if err := scanOperation(row, &op); err != nil {
		rejectErr(http.StatusInternalServerError, "operation_insert_failed", "failed to create operation")
		return
	}

	audit(op.ID, auditOutcomeSuccess, nil)

	c.JSON(http.StatusAccepted, gin.H{"operation": op, "message": "Project deletion queued"})
}

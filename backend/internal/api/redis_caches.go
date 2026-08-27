package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// serviceCacheProfiles are the capability profiles ServiceCacheV2 accepts,
// mirroring the enum in argo-infra's servicecache-xrd.yaml exactly -- kept
// in sync by hand since the console has no live schema introspection of the
// XRD. See provider-redis's docs/capability-profiles-addendum.md for what
// each one grants.
var serviceCacheProfiles = map[string]bool{
	"redis-kv-readonly":     true,
	"redis-kv-readwrite":    true,
	"redis-stream-producer": true,
	"redis-stream-consumer": true,
	"redis-stream-admin":    true,
	"redis-list-producer":   true,
	"redis-list-consumer":   true,
}

// ListServiceCaches returns all ServiceCacheV2 resources in a project environment.
//
// @ID          listServiceCaches
// @Summary     List managed Redis cache users in an environment
// @Description Returns all managed Redis ACL users (ServiceCacheV2) in the given project environment, with their live phase/status.
// @Tags        cache
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Success     200       {object} map[string]interface{} "object with a caches array of ResourceSnapshot"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/caches [get]
func (h *Handler) ListServiceCaches(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}

	_, err := h.effectiveRole(c.Request.Context(), claims, projectID)
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
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceCacheV2'
		 ORDER BY name`,
		projectID, envID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query caches")
		return
	}
	defer rows.Close()

	var caches []models.ResourceSnapshot
	for rows.Next() {
		var rs models.ResourceSnapshot
		if err := rows.Scan(
			&rs.ID, &rs.ProjectID, &rs.EnvironmentID, &rs.Kind, &rs.Name,
			&rs.Phase, &rs.SummaryJSON, &rs.LastSyncedAt,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan cache")
			return
		}
		caches = append(caches, rs)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading caches")
		return
	}
	if caches == nil {
		caches = []models.ResourceSnapshot{}
	}

	c.JSON(http.StatusOK, gin.H{"caches": caches})
}

type createServiceCacheRequest struct {
	Name      string `json:"name"`
	AppRef    string `json:"app_ref"`
	KeyPrefix string `json:"key_prefix"`
	Profile   string `json:"profile"`
}

// CreateServiceCache enqueues an operation to provision a new ServiceCacheV2 CRD.
//
// @ID          createServiceCache
// @Summary     Order a managed Redis cache user
// @Description Provisions a scoped Redis ACL user (ServiceCacheV2) on the shared managed-Redis instance, bound to app_ref. Asynchronous: returns 202 with an operation; poll the operation until it reaches a terminal status.
// @Tags        cache
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                     true "Project UUID"
// @Param       envId     path     string                     true "Environment UUID"
// @Param       body      body     createServiceCacheRequest  true "Cache user specification"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/caches [post]
func (h *Handler) CreateServiceCache(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}

	var req createServiceCacheRequest
	audit := func(opID uuid.UUID, outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			OperationID:   opID,
			Action:        "CreateServiceCache",
			ResourceKind:  "ServiceCacheV2",
			ResourceName:  req.Name,
			Outcome:       outcome,
			Metadata:      meta,
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

	if orgID, orgErr := h.projectOrg(c.Request.Context(), projectID); orgErr == nil {
		if qErr := h.checkQuota(c.Request.Context(), orgID, "caches"); qErr != nil {
			if meta, blocked := billingBlockAudit(qErr); blocked {
				meta["status"] = http.StatusPaymentRequired
				audit(uuid.Nil, auditOutcomeFailure, meta)
				h.respondBillingBlocked(c, orgID, qErr)
				return
			}
		}
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		rejectErr(http.StatusBadRequest, "malformed_body", err.Error())
		return
	}
	if req.Name == "" {
		rejectErr(http.StatusBadRequest, "name_required", "name is required")
		return
	}
	if err := validateKubeName(req.Name); err != nil {
		rejectErr(http.StatusBadRequest, "invalid_name", err.Error())
		return
	}
	// app_ref is required (unlike ServiceDatabaseV2's optional appRef): a cache
	// user has no standalone owner chart, see models.CreateServiceCachePayload.
	if req.AppRef == "" {
		rejectErr(http.StatusBadRequest, "app_ref_required", "app_ref is required")
		return
	}
	if err := validateKubeName(req.AppRef); err != nil {
		rejectErr(http.StatusBadRequest, "invalid_app_ref", "app_ref must be a bare app name (lowercase alphanumeric with hyphens, max 63 chars): "+err.Error())
		return
	}
	if req.KeyPrefix == "" {
		rejectErr(http.StatusBadRequest, "key_prefix_required", "key_prefix is required")
		return
	}
	if err := validateKeyPrefix(req.KeyPrefix); err != nil {
		rejectErr(http.StatusBadRequest, "invalid_key_prefix", err.Error())
		return
	}
	if !serviceCacheProfiles[req.Profile] {
		rejectErr(http.StatusBadRequest, "invalid_profile",
			fmt.Sprintf("profile must be one of: %s", joinProfileNames()))
		return
	}

	var existing int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceCacheV2' AND name = $3`,
		projectID, envID, req.Name,
	).Scan(&existing); err != nil {
		rejectErr(http.StatusInternalServerError, "uniqueness_check_failed", "failed to check name uniqueness")
		return
	}
	if existing > 0 {
		rejectErr(http.StatusConflict, "name_taken", "a cache user with that name already exists in this environment")
		return
	}

	shard := h.placeTenantCacheShard(c.Request.Context())

	payload := models.CreateServiceCachePayload{
		Name:      req.Name,
		AppRef:    req.AppRef,
		KeyPrefix: req.KeyPrefix,
		Profile:   req.Profile,
		Shard:     shard,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		rejectErr(http.StatusInternalServerError, "payload_marshal_failed", "failed to marshal payload")
		return
	}

	tx, err := h.pool.Begin(c.Request.Context())
	if err != nil {
		rejectErr(http.StatusInternalServerError, "tx_begin_failed", "failed to create operation")
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	var op models.Operation
	row := tx.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'CreateServiceCache', 'ServiceCacheV2', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, req.Name, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		rejectErr(http.StatusInternalServerError, "operation_insert_failed", "failed to create operation")
		return
	}

	if err = seedOptimisticSnapshot(c.Request.Context(), tx, projectID, envID, "ServiceCacheV2", req.Name, map[string]any{
		"name": req.Name,
		"kind": "ServiceCacheV2",
		"spec": map[string]any{
			"appRef":    req.AppRef,
			"keyPrefix": req.KeyPrefix,
			"profile":   req.Profile,
			"shard":     shard,
		},
	}); err != nil {
		rejectErr(http.StatusInternalServerError, "snapshot_seed_failed", "failed to create operation")
		return
	}

	if err = tx.Commit(c.Request.Context()); err != nil {
		rejectErr(http.StatusInternalServerError, "tx_commit_failed", "failed to create operation")
		return
	}

	audit(op.ID, auditOutcomeSuccess, map[string]any{
		"app_ref":    req.AppRef,
		"key_prefix": req.KeyPrefix,
		"profile":    req.Profile,
		"shard":      shard,
	})
	h.notifyAuditEvent(claims, projectID, "CreateServiceCache", req.Name)

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"message":   "ServiceCache creation queued",
	})
}

// DeleteServiceCache enqueues an operation to tear down a managed Redis cache user.
//
// @ID          deleteServiceCache
// @Summary     Delete a managed Redis cache user
// @Description Destructive: permanently removes a managed Redis ACL user (ServiceCacheV2). The agent drops the CR entry from git and Argo prunes it. Asynchronous: returns 202 with an operation; poll the operation until terminal.
// @Tags        cache
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Cache resource name"
// @Success     202       {object} map[string]interface{} "object with the accepted operation"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/caches/{name} [delete]
func (h *Handler) DeleteServiceCache(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	name := c.Param("name")

	audit := func(opID uuid.UUID, outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			OperationID:   opID,
			Action:        "DeleteServiceCache",
			ResourceKind:  "ServiceCacheV2",
			ResourceName:  name,
			Outcome:       outcome,
			Metadata:      meta,
		})
	}
	rejectErr := func(status int, reason, msg string) {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": reason, "status": status})
		respondError(c, status, msg)
	}

	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "not_a_writer"})
		return
	}

	var summaryRaw []byte
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceCacheV2' AND name = $3`,
		projectID, envID, name,
	).Scan(&summaryRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "not_found", "status": http.StatusNotFound})
		respondNotFound(c)
		return
	}
	if err != nil {
		rejectErr(http.StatusInternalServerError, "lookup_failed", "failed to look up cache")
		return
	}

	appRef := serviceCacheAppRef(summaryRaw)

	payload := models.DeleteServiceCachePayload{Name: name, AppRef: appRef}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		rejectErr(http.StatusInternalServerError, "payload_marshal_failed", "failed to marshal payload")
		return
	}

	var op models.Operation
	row := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'DeleteServiceCache', 'ServiceCacheV2', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, name, payloadBytes,
	)
	if err = scanOperation(row, &op); err != nil {
		rejectErr(http.StatusInternalServerError, "operation_insert_failed", "failed to create operation")
		return
	}

	audit(op.ID, auditOutcomeSuccess, map[string]any{"app_ref": appRef})

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"message":   "ServiceCache deletion queued",
	})
}

// serviceCacheAppRef pulls spec.appRef from a ServiceCacheV2 snapshot's
// summary_json. Mirrors serviceDatabaseAppRef.
func serviceCacheAppRef(summaryRaw []byte) string {
	var summary map[string]any
	if json.Unmarshal(summaryRaw, &summary) != nil {
		return ""
	}
	spec, ok := summary["spec"].(map[string]any)
	if !ok {
		return ""
	}
	appRef, _ := spec["appRef"].(string)
	return appRef
}

// serviceCacheNamespace pulls spec.namespace from a ServiceCacheV2 snapshot's
// summary_json. Mirrors serviceDatabaseNamespace.
func serviceCacheNamespace(summaryRaw []byte) string {
	var summary map[string]any
	if json.Unmarshal(summaryRaw, &summary) != nil {
		return ""
	}
	if spec, ok := summary["spec"].(map[string]any); ok {
		if ns, _ := spec["namespace"].(string); ns != "" {
			return ns
		}
	}
	ns, _ := summary["namespace"].(string)
	return ns
}

// GetServiceCacheCredentials reveals a managed Redis cache user's connection
// credentials by reading its Crossplane connection secret on demand.
// Mirrors GetDatabaseCredentials.
//
// @ID          getServiceCacheCredentials
// @Summary     Reveal a Redis cache user's connection credentials
// @Description Reveals the host, port, username and password for a managed Redis ACL user (ServiceCacheV2) by reading its Crossplane connection secret. Requires reveal=true and write access; every reveal is audited. Returns 404 while the cache user is still provisioning (no secret yet) and 503 when in-cluster credential access is not configured.
// @Tags        cache
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       name      path     string true "Cache resource name"
// @Param       reveal    query    bool   true "Must be true to reveal the credentials"
// @Success     200       {object} map[string]interface{} "object with host, port, username, password and a ready-to-paste dsn"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/caches/{name}/credentials [get]
func (h *Handler) GetServiceCacheCredentials(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	name := c.Param("name")

	audit := func(outcome string, meta map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "database.revealRedisCredentials",
			ResourceKind:  "ServiceCacheV2",
			ResourceName:  name,
			Outcome:       outcome,
			Metadata:      meta,
		})
	}
	rejectErr := func(status int, reason, msg string) {
		audit(auditOutcomeFailure, map[string]any{"reason": reason, "status": status})
		respondError(c, status, msg)
	}

	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		audit(auditOutcomeFailure, map[string]any{"reason": "not_a_writer"})
		return
	}
	if c.Query("reveal") != "true" {
		rejectErr(http.StatusBadRequest, "reveal_not_confirmed", "reveal=true is required")
		return
	}

	var summaryRaw []byte
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceCacheV2' AND name = $3`,
		projectID, envID, name,
	).Scan(&summaryRaw); err != nil {
		if err == pgx.ErrNoRows {
			audit(auditOutcomeFailure, map[string]any{"reason": "not_found", "status": http.StatusNotFound})
			respondNotFound(c)
			return
		}
		rejectErr(http.StatusInternalServerError, "lookup_failed", "failed to look up cache")
		return
	}

	// The connection secret is named "<appRef>-<name>-redis-credentials"
	// (the Composition ties the secret name to both spec.appRef and the XR's
	// own name, unlike ServiceDatabaseV2's "<appRef>-db-credentials" -- a
	// single app can hold multiple cache users, one per capability profile,
	// so appRef alone would collide).
	namespace := serviceCacheNamespace(summaryRaw)
	appRef := serviceCacheAppRef(summaryRaw)
	secretName := fmt.Sprintf("%s-%s-redis-credentials", appRef, name)

	creds, err := h.rediscreds.Resolve(c.Request.Context(), namespace, secretName)
	if err != nil {
		if errors.Is(err, cloudtask.ErrRedisCredentialsNotReady) {
			rejectErr(http.StatusNotFound, "secret_not_ready", "credentials not available yet — the cache user is still provisioning")
			return
		}
		rejectErr(http.StatusServiceUnavailable, "credential_access_unconfigured", "redis credential access is not configured for this environment")
		return
	}

	host := creds.Endpoint
	port := creds.Port
	if port == "" {
		port = "6379"
	}
	dsn := redisDSN(creds.Username, creds.Password, host, port)

	audit(auditOutcomeSuccess, map[string]any{"revealed": true, "namespace": namespace})

	c.JSON(http.StatusOK, gin.H{
		"host":     host,
		"port":     port,
		"username": creds.Username,
		"password": creds.Password,
		"dsn":      dsn,
	})
}

// redisDSN assembles a ready-to-paste redis:// connection string, mirroring
// postgresDSN's reasoning: users handed only host/port/user/password
// assemble it by hand and get it wrong. url.UserPassword percent-encodes
// credentials, matters here too since generated passwords may carry
// characters that are structural in a URL.
func redisDSN(username, password, host, port string) string {
	if host == "" {
		return ""
	}
	if port == "" {
		port = "6379"
	}
	u := url.URL{
		Scheme: "redis",
		User:   url.UserPassword(username, password),
		Host:   net.JoinHostPort(host, port),
	}
	return u.String()
}

// joinProfileNames renders serviceCacheProfiles' keys for an error message,
// in a stable order.
func joinProfileNames() string {
	names := make([]string, 0, len(serviceCacheProfiles))
	for k := range serviceCacheProfiles {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// placeTenantCacheShard resolves the Redis instance a new ServiceCacheV2
// lands on. Mirrors placeTenantDatabaseShard's contract (empty return = XRD
// default) but there is only ever one shard today (shard-0, see argo-infra's
// crossplane-platform-api values.yaml serviceCache.shards) -- returning ""
// unconditionally lets the XRD default carry it, exactly like a
// single-shard Postgres deployment would, and this only needs a real
// placement policy once a second Redis instance exists.
func (h *Handler) placeTenantCacheShard(ctx context.Context) string {
	return ""
}

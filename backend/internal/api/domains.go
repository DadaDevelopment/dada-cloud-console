package api

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// Custom domains (Vercel-style two-level model). Level 1: a project proves
// ownership of an apex domain once via a TXT challenge (domain_authorizations).
// Level 2: a hostname (apex or subdomain) under a verified apex is attached to an
// app/env, routed by a native Ingress + cert-manager cert (domain_hostnames).
// No Beget / PublicApi — external zones are owned by the user.

const domainVerifyPrefix = "dada-domain-verify="

// txtChallengeValue is the full TXT record value the user must publish.
func txtChallengeValue(token string) string {
	return domainVerifyPrefix + token
}

// txtChallengeHost is the host the TXT record lives at: <label>.<apex>.
func txtChallengeHost(cfg *config.Config, apex string) string {
	return cfg.CustomDomainVerifyLabel + "." + apex
}

// challengeInfo is the DNS instructions the console shows for an authorization.
type challengeInfo struct {
	Type  string `json:"type"`  // "TXT"
	Host  string `json:"host"`  // _dada-verify.acme.com
	Value string `json:"value"` // dada-domain-verify=<token>
}

func (h *Handler) authChallenge(a *models.DomainAuthorization) challengeInfo {
	return challengeInfo{
		Type:  "TXT",
		Host:  txtChallengeHost(h.cfg, a.ApexDomain),
		Value: txtChallengeValue(a.VerificationToken),
	}
}

// ── Level 1: domain authorizations ───────────────────────────────────────────

type addDomainAuthorizationRequest struct {
	ApexDomain string `json:"apex_domain"`
}

// AddDomainAuthorization registers an apex domain for a project and returns the
// TXT challenge the user must publish to prove ownership.
//
// @ID          addDomainAuthorization
// @Summary     Authorize an apex domain for a project
// @Description Registers a user-owned apex domain (e.g. acme.com) and returns a TXT challenge. Once the record is published and verified, the project may attach that apex and any of its subdomains to its deployments.
// @Tags        domain
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                        true "Project UUID"
// @Param       body      body     addDomainAuthorizationRequest true "Apex domain"
// @Success     201       {object} map[string]interface{}
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Router      /projects/{projectId}/domain-authorizations [post]
func (h *Handler) AddDomainAuthorization(c *gin.Context) {
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

	if orgID, orgErr := h.projectOrg(c.Request.Context(), projectID); orgErr == nil {
		if qErr := h.checkQuota(c.Request.Context(), orgID, "domains"); qErr != nil {
			if qe, ok := qErr.(*quotaExceededError); ok {
				respondQuotaExceeded(c, qe.Resource, qe.Limit)
				return
			}
		}
	}

	var req addDomainAuthorizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	apex := normalizeDomain(req.ApexDomain)
	if !isValidDomain(apex) {
		respondError(c, http.StatusBadRequest, "apex_domain must be a valid domain name")
		return
	}

	token, err := randomToken()
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to generate token")
		return
	}

	var a models.DomainAuthorization
	err = h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO domain_authorizations (project_id, apex_domain, verification_token, created_by)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, project_id, apex_domain, verification_token, status, verified_at,
		           last_checked_at, error_message, created_by, created_at, updated_at`,
		projectID, apex, token, claims.UserID,
	).Scan(
		&a.ID, &a.ProjectID, &a.ApexDomain, &a.VerificationToken, &a.Status,
		&a.VerifiedAt, &a.LastCheckedAt, &a.ErrorMessage, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt,
	)
	if isUniqueViolation(err) {
		respondError(c, http.StatusConflict, "that apex domain is already authorized by a project")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create authorization")
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"authorization": a,
		"challenge":     h.authChallenge(&a),
	})
}

// ListDomainAuthorizations returns all apex authorizations for a project.
//
// @ID          listDomainAuthorizations
// @Summary     List apex domain authorizations
// @Tags        domain
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Success     200       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/domain-authorizations [get]
func (h *Handler) ListDomainAuthorizations(c *gin.Context) {
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
	if _, err := h.effectiveRole(c.Request.Context(), claims, projectID); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, project_id, apex_domain, verification_token, status, verified_at,
		        last_checked_at, error_message, created_by, created_at, updated_at
		 FROM domain_authorizations WHERE project_id = $1 ORDER BY apex_domain`,
		projectID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query authorizations")
		return
	}
	defer rows.Close()

	type authWithChallenge struct {
		models.DomainAuthorization
		Challenge challengeInfo `json:"challenge"`
	}
	out := []authWithChallenge{}
	for rows.Next() {
		var a models.DomainAuthorization
		if err := rows.Scan(
			&a.ID, &a.ProjectID, &a.ApexDomain, &a.VerificationToken, &a.Status,
			&a.VerifiedAt, &a.LastCheckedAt, &a.ErrorMessage, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan authorization")
			return
		}
		out = append(out, authWithChallenge{DomainAuthorization: a, Challenge: h.authChallenge(&a)})
	}
	c.JSON(http.StatusOK, gin.H{"authorizations": out})
}

// VerifyDomainAuthorization runs the TXT check immediately (instead of waiting
// for the background poller) and returns the updated authorization.
//
// @ID          verifyDomainAuthorization
// @Summary     Force a DNS verification check now
// @Tags        domain
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       id        path     string true "Authorization UUID"
// @Success     200       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/domain-authorizations/{id}/verify [post]
func (h *Handler) VerifyDomainAuthorization(c *gin.Context) {
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
	authID, err := uuid.Parse(c.Param("id"))
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

	var a models.DomainAuthorization
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT id, project_id, apex_domain, verification_token, status, verified_at,
		        last_checked_at, error_message, created_by, created_at, updated_at
		 FROM domain_authorizations WHERE id = $1 AND project_id = $2`,
		authID, projectID,
	).Scan(
		&a.ID, &a.ProjectID, &a.ApexDomain, &a.VerificationToken, &a.Status,
		&a.VerifiedAt, &a.LastCheckedAt, &a.ErrorMessage, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load authorization")
		return
	}

	verifyAuthorization(c.Request.Context(), h.pool, h.cfg, &a)

	c.JSON(http.StatusOK, gin.H{
		"authorization": a,
		"challenge":     h.authChallenge(&a),
	})
}

// DeleteDomainAuthorization removes an apex authorization and cascades its
// attached hostnames. Detaching the live Ingress for each hostname is the user's
// responsibility first; this only removes the authorization records.
//
// @ID          deleteDomainAuthorization
// @Summary     Remove an apex domain authorization
// @Tags        domain
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       id        path     string true "Authorization UUID"
// @Success     204
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Router      /projects/{projectId}/domain-authorizations/{id} [delete]
func (h *Handler) DeleteDomainAuthorization(c *gin.Context) {
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
	authID, err := uuid.Parse(c.Param("id"))
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

	hnRows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, environment_id, app_name, hostname FROM domain_hostnames WHERE authorization_id = $1`, authID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load attached hostnames")
		return
	}
	type attachedHostname struct {
		id      uuid.UUID
		envID   uuid.UUID
		appName string
		host    string
	}
	var attached []attachedHostname
	for hnRows.Next() {
		var a attachedHostname
		if err := hnRows.Scan(&a.id, &a.envID, &a.appName, &a.host); err != nil {
			hnRows.Close()
			respondError(c, http.StatusInternalServerError, "failed to scan attached hostname")
			return
		}
		attached = append(attached, a)
	}
	hnRows.Close()

	for _, a := range attached {
		payload := models.DetachCustomHostnamePayload{AppName: a.appName, Hostname: a.host}
		payloadBytes, mErr := json.Marshal(payload)
		if mErr != nil {
			respondError(c, http.StatusInternalServerError, "failed to marshal detach payload")
			return
		}
		var opID uuid.UUID
		if err := h.pool.QueryRow(c.Request.Context(),
			`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
			 VALUES ($1, $2, $3, 'DetachCustomHostname', 'CustomDomain', $4, 'Created', $5) RETURNING id`,
			claims.UserID, projectID, a.envID, a.host, payloadBytes,
		).Scan(&opID); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to queue hostname detachment")
			return
		}
		if _, err := h.pool.Exec(c.Request.Context(),
			`DELETE FROM domain_hostnames WHERE id = $1`, a.id); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to remove attached hostname")
			return
		}
		auditMeta, _ := json.Marshal(payload)
		_, _ = h.pool.Exec(c.Request.Context(),
			`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
			 VALUES ($1, $2, $3, 'DetachCustomHostname', 'CustomDomain', $4, $5)`,
			claims.UserID, projectID, opID, a.host, auditMeta,
		)
	}

	ct, err := h.pool.Exec(c.Request.Context(),
		`DELETE FROM domain_authorizations WHERE id = $1 AND project_id = $2`, authID, projectID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to delete authorization")
		return
	}
	if ct.RowsAffected() == 0 {
		respondNotFound(c)
		return
	}
	c.Status(http.StatusNoContent)
}

// ── Level 2: hostname attachments ────────────────────────────────────────────

type attachHostnameRequest struct {
	Hostname string `json:"hostname"`
}

// AttachHostname attaches a hostname (apex or subdomain) under a verified
// authorization to an app, enqueuing an AttachCustomHostname operation.
//
// @ID          attachHostname
// @Summary     Attach a custom hostname to an app
// @Description Attaches a hostname (the apex itself or any subdomain) under a verified apex authorization to an app/environment. Returns the DNS record the user must point at the platform LB, plus a 202 operation that provisions the Ingress + TLS cert.
// @Tags        domain
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                true "Project UUID"
// @Param       envId     path     string                true "Environment UUID"
// @Param       appName   path     string                true "App name"
// @Param       body      body     attachHostnameRequest true "Hostname"
// @Success     202       {object} map[string]interface{}
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/hostnames [post]
func (h *Handler) AttachHostname(c *gin.Context) {
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

	var req attachHostnameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	hostname := normalizeDomain(req.Hostname)
	if !isValidDomain(hostname) {
		respondError(c, http.StatusBadRequest, "hostname must be a valid domain name")
		return
	}

	// Anti-hijack: the hostname's apex must be a VERIFIED authorization for THIS
	// project. Pick the most specific matching apex (longest suffix).
	var authID uuid.UUID
	var apex string
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT id, apex_domain FROM domain_authorizations
		 WHERE project_id = $1 AND status = 'verified'
		   AND ($2 = apex_domain OR $2 LIKE '%.' || apex_domain)
		 ORDER BY length(apex_domain) DESC LIMIT 1`,
		projectID, hostname,
	).Scan(&authID, &apex)
	if err == pgx.ErrNoRows {
		respondError(c, http.StatusForbidden, "no verified apex authorization covers this hostname")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check authorization")
		return
	}

	// App must exist in this environment.
	var appCount int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&appCount); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify app")
		return
	}
	if appCount == 0 {
		respondError(c, http.StatusNotFound, "app not found")
		return
	}

	recordType := "CNAME"
	if hostname == apex {
		recordType = "A"
	}

	payload := models.AttachCustomHostnamePayload{AppName: appName, Hostname: hostname}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to marshal payload")
		return
	}

	var op models.Operation
	err = scanOperation(h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'AttachCustomHostname', 'CustomDomain', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, hostname, payloadBytes,
	), &op)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	var hn models.DomainHostname
	err = h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO domain_hostnames (authorization_id, environment_id, app_name, hostname, record_type, operation_id)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, authorization_id, environment_id, app_name, hostname, record_type,
		           status, cert_status, operation_id, created_at, updated_at`,
		authID, envID, appName, hostname, recordType, op.ID,
	).Scan(
		&hn.ID, &hn.AuthorizationID, &hn.EnvironmentID, &hn.AppName, &hn.Hostname, &hn.RecordType,
		&hn.Status, &hn.CertStatus, &hn.OperationID, &hn.CreatedAt, &hn.UpdatedAt,
	)
	if isUniqueViolation(err) {
		respondError(c, http.StatusConflict, "that hostname is already attached")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to record hostname")
		return
	}

	auditMeta, _ := json.Marshal(payload)
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, 'AttachCustomHostname', 'CustomDomain', $4, $5)`,
		claims.UserID, projectID, op.ID, hostname, auditMeta,
	)
	h.notifyAuditEvent(claims, projectID, "AttachCustomHostname", hostname)

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"hostname":  hn,
		"dns_record": gin.H{
			"type":   recordType,
			"host":   hostname,
			"target": h.recordTarget(recordType),
		},
		"message": "Hostname attachment queued",
	})
}

// recordTarget returns the value the user must point their DNS record at.
func (h *Handler) recordTarget(recordType string) string {
	if recordType == "A" {
		return h.cfg.CustomDomainATarget
	}
	return h.cfg.CustomDomainCNAMETarget
}

// ListHostnames returns the custom hostnames attached to an app in an environment.
//
// @ID          listHostnames
// @Summary     List custom hostnames attached to an app
// @Tags        domain
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/hostnames [get]
func (h *Handler) ListHostnames(c *gin.Context) {
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

	if _, err := h.effectiveRole(c.Request.Context(), claims, projectID); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	} else if err != nil {
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

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, authorization_id, managed, environment_id, app_name, hostname, record_type,
		        status, cert_status, operation_id, created_at, updated_at
		 FROM domain_hostnames
		 WHERE environment_id = $1 AND app_name = $2 ORDER BY managed DESC, hostname`,
		envID, appName,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query hostnames")
		return
	}
	defer rows.Close()

	out := []models.DomainHostname{}
	for rows.Next() {
		var hn models.DomainHostname
		if err := rows.Scan(
			&hn.ID, &hn.AuthorizationID, &hn.Managed, &hn.EnvironmentID, &hn.AppName, &hn.Hostname, &hn.RecordType,
			&hn.Status, &hn.CertStatus, &hn.OperationID, &hn.CreatedAt, &hn.UpdatedAt,
		); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan hostname")
			return
		}
		out = append(out, hn)
	}
	c.JSON(http.StatusOK, gin.H{"hostnames": out})
}

// DetachHostname enqueues a DetachCustomHostname operation and removes the
// hostname record once queued.
//
// @ID          detachHostname
// @Summary     Detach a custom hostname from an app
// @Tags        domain
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Param       id        path     string true "Hostname UUID"
// @Success     202       {object} map[string]interface{}
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/hostnames/{id} [delete]
func (h *Handler) DetachHostname(c *gin.Context) {
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
	hostnameID, err := uuid.Parse(c.Param("id"))
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

	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to verify environment")
		return
	} else if !ok {
		respondNotFound(c)
		return
	}

	var hn models.DomainHostname
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT id, authorization_id, managed, environment_id, app_name, hostname, record_type,
		        status, cert_status, operation_id, created_at, updated_at
		 FROM domain_hostnames WHERE id = $1 AND environment_id = $2 AND app_name = $3`,
		hostnameID, envID, appName,
	).Scan(
		&hn.ID, &hn.AuthorizationID, &hn.Managed, &hn.EnvironmentID, &hn.AppName, &hn.Hostname, &hn.RecordType,
		&hn.Status, &hn.CertStatus, &hn.OperationID, &hn.CreatedAt, &hn.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load hostname")
		return
	}
	if hn.Managed {
		respondError(c, http.StatusConflict, "the default domain cannot be detached")
		return
	}

	payload := models.DetachCustomHostnamePayload{AppName: appName, Hostname: hn.Hostname}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to marshal payload")
		return
	}

	var op models.Operation
	err = scanOperation(h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'DetachCustomHostname', 'CustomDomain', $4, 'Created', $5)
		 RETURNING id, actor_id, project_id, environment_id, action, resource_kind, resource_name,
		           status, payload, validation_result, git_commit, git_path, argo_application,
		           error_code, error_message, created_at, updated_at`,
		claims.UserID, projectID, envID, hn.Hostname, payloadBytes,
	), &op)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to create operation")
		return
	}

	// Drop the hostname record now that teardown is queued.
	_, _ = h.pool.Exec(c.Request.Context(), `DELETE FROM domain_hostnames WHERE id = $1`, hostnameID)

	auditMeta, _ := json.Marshal(payload)
	_, _ = h.pool.Exec(c.Request.Context(),
		`INSERT INTO audit_events (actor_id, project_id, operation_id, action, resource_kind, resource_name, metadata)
		 VALUES ($1, $2, $3, 'DetachCustomHostname', 'CustomDomain', $4, $5)`,
		claims.UserID, projectID, op.ID, hn.Hostname, auditMeta,
	)

	c.JSON(http.StatusAccepted, gin.H{
		"operation": op,
		"message":   "Hostname detachment queued",
	})
}

// ── DNS verification ─────────────────────────────────────────────────────────

// verifyAuthorization resolves the TXT challenge for one authorization and
// updates its status in place (and in the DB). Idempotent and side-effect-safe:
// safe to call from both the manual endpoint and the background poller.
func verifyAuthorization(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config, a *models.DomainAuthorization) {
	host := txtChallengeHost(cfg, a.ApexDomain)
	want := txtChallengeValue(a.VerificationToken)

	lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var resolver net.Resolver
	records, err := resolver.LookupTXT(lookupCtx, host)

	now := time.Now()
	matched := false
	for _, r := range records {
		if strings.TrimSpace(r) == want {
			matched = true
			break
		}
	}

	switch {
	case matched:
		a.Status = "verified"
		a.VerifiedAt = &now
		a.LastCheckedAt = &now
		a.ErrorMessage = ""
		_, _ = pool.Exec(ctx,
			`UPDATE domain_authorizations
			 SET status='verified', verified_at=$2, last_checked_at=$2, error_message='', updated_at=NOW()
			 WHERE id=$1`,
			a.ID, now,
		)
	default:
		msg := fmt.Sprintf("TXT record %s not found at %s", want, host)
		if err != nil {
			msg = fmt.Sprintf("DNS lookup failed for %s: %v", host, err)
		}
		// Don't downgrade a previously-verified domain on a transient miss.
		if a.Status != "verified" {
			a.Status = "pending"
			a.LastCheckedAt = &now
			a.ErrorMessage = msg
			_, _ = pool.Exec(ctx,
				`UPDATE domain_authorizations
				 SET last_checked_at=$2, error_message=$3, updated_at=NOW()
				 WHERE id=$1 AND status <> 'verified'`,
				a.ID, now, msg,
			)
		} else {
			a.LastCheckedAt = &now
			_, _ = pool.Exec(ctx,
				`UPDATE domain_authorizations SET last_checked_at=$2, updated_at=NOW() WHERE id=$1`,
				a.ID, now,
			)
		}
	}
}

// VerifyPendingDomains is the background poller body: it re-checks every
// not-yet-verified authorization. Called on a ticker from main.
func VerifyPendingDomains(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config) error {
	rows, err := pool.Query(ctx,
		`SELECT id, project_id, apex_domain, verification_token, status, verified_at,
		        last_checked_at, error_message, created_by, created_at, updated_at
		 FROM domain_authorizations WHERE status <> 'verified'`,
	)
	if err != nil {
		return err
	}
	var pending []models.DomainAuthorization
	for rows.Next() {
		var a models.DomainAuthorization
		if err := rows.Scan(
			&a.ID, &a.ProjectID, &a.ApexDomain, &a.VerificationToken, &a.Status,
			&a.VerifiedAt, &a.LastCheckedAt, &a.ErrorMessage, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, a)
	}
	rows.Close()

	for i := range pending {
		verifyAuthorization(ctx, pool, cfg, &pending[i])
	}
	return nil
}

// hostnamePendingFailAfter bounds how long a custom hostname may sit pending
// before it is declared failed. A genuine attach (user points DNS at our LB,
// cert-manager solves HTTP-01) completes in minutes to a few hours, so anything
// still pending after this window is not "attaching", it is stuck for a reason
// that will not clear on its own: the owning app was retired, or its Ingress was
// dropped from git out-of-band (e.g. a hand-written "retire app" commit that
// bypassed DetachHostname, leaving this row orphaned). Without a terminal state
// such a row probes a never-issued cert forever and pins
// dada_domain_hostname_pending_age_seconds high, firing DadaCustomDomainStuck
// indefinitely. Failing it clears the alert (the gauge counts only status
// 'pending'), keeps the row visible in the console, and lets the user re-attach
// to retry. Deliberately generous so a slow-but-real DNS cutover is never failed.
const hostnamePendingFailAfter = 48 * time.Hour

// hostnamePendingExpired reports whether a hostname that has been pending since
// createdAt has exceeded the attach window as of now and should be failed.
func hostnamePendingExpired(createdAt, now time.Time) bool {
	return now.Sub(createdAt) > hostnamePendingFailAfter
}

// hostnameDNSStuckAfter bounds how long a managed (surrogate) hostname may sit
// pending with its A record unresolved before ReconcilePendingHostnames treats
// the original DNS write as lost and re-issues it. A genuine write is visible
// in public DNS within seconds to low minutes, so anything still unresolved
// past this window is not "propagating", it is a write that never landed --
// e.g. dropped during a Beget-API egress block window. Long enough that
// ordinary propagation lag never triggers a spurious re-issue.
const hostnameDNSStuckAfter = 4 * time.Minute

// hostnameReissueCooldown bounds how often a single hostname may be re-issued.
// The failure mode this guards against is itself caused by egress pressure on
// the Beget API, so a stuck row must not be re-driven every reconcile tick --
// that would add to the exact load that caused the record to be lost. One
// re-issue per cooldown window is enough to recover once the block clears
// without turning the reconciler into a hammer.
const hostnameReissueCooldown = 15 * time.Minute

// hostnameDNSLookupTimeout bounds each verify-resolve check.
const hostnameDNSLookupTimeout = 3 * time.Second

// reissueActorID is the fixed system-user id (see migration 010_system_user.sql)
// used as actor_id for operations the reconciler enqueues on its own, with no
// human actor behind them.
var reissueActorID = uuid.MustParse("00000000-0000-0000-0000-000000000000")

// ReconcilePendingHostnames flips a hostname from pending to active once its
// Let's Encrypt certificate is serving end-to-end, fails hostnames that have
// been pending past hostnamePendingFailAfter, and -- for managed (surrogate)
// hostnames only -- re-issues the DNS write when the A record itself never
// resolved within hostnameDNSStuckAfter. Nothing else updates the row after
// AttachHostname/CreateApp commits the Ingress (and, for managed rows, the
// PublicApi DNS composite) to git, so without this a fully working domain
// shows "pending" forever in the console, and a managed hostname whose DNS
// write was dropped (e.g. a Beget-API egress block at write time) stays
// NXDOMAIN forever with no auto-recovery.
//
// The cert probe is an external TLS handshake to hostname:443 with SNI: it
// only succeeds when the leaf cert is publicly trusted (LE issued) and valid
// for the hostname, which proves DNS -> ingress -> cert all resolved. A failed
// probe within the attach window leaves the row pending to be retried on the
// next tick; past the window the row is marked failed. Both UPDATEs are
// guarded on status='pending' so a concurrent detach or a row that just went
// active is never clobbered.
func ReconcilePendingHostnames(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config) error {
	rows, err := pool.Query(ctx,
		`SELECT dh.id, dh.hostname, dh.created_at, dh.managed, dh.environment_id, dh.app_name,
		        dh.last_reissue_at, e.project_id
		   FROM domain_hostnames dh
		   JOIN environments e ON e.id = dh.environment_id
		  WHERE dh.status = 'pending'`)
	if err != nil {
		return err
	}
	type pendingHost struct {
		id            uuid.UUID
		hostname      string
		createdAt     time.Time
		managed       bool
		environmentID uuid.UUID
		appName       string
		lastReissue   *time.Time
		projectID     uuid.UUID
	}
	var pending []pendingHost
	for rows.Next() {
		var p pendingHost
		if err := rows.Scan(&p.id, &p.hostname, &p.createdAt, &p.managed, &p.environmentID,
			&p.appName, &p.lastReissue, &p.projectID); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, p)
	}
	rows.Close()

	now := time.Now()
	for _, p := range pending {
		if hostnameCertLive(ctx, p.hostname) {
			_, _ = pool.Exec(ctx,
				`UPDATE domain_hostnames SET status='active', cert_status='active', updated_at=now()
				  WHERE id=$1 AND status='pending'`, p.id)
			continue
		}
		if hostnamePendingExpired(p.createdAt, now) {
			ct, err := pool.Exec(ctx,
				`UPDATE domain_hostnames SET status='failed', cert_status='failed', updated_at=now()
				  WHERE id=$1 AND status='pending'`, p.id)
			if err == nil && ct.RowsAffected() > 0 {
				log.Warn().
					Str("hostname", p.hostname).
					Dur("pending_for", time.Since(p.createdAt)).
					Msg("hostname failed: pending past attach window (app retired or Ingress missing) -- re-attach to retry")
			}
			continue
		}
		if !p.managed || cfg == nil || cfg.ClusterLBIP == "" {
			continue
		}
		if now.Sub(p.createdAt) <= hostnameDNSStuckAfter {
			continue
		}
		if p.lastReissue != nil && now.Sub(*p.lastReissue) <= hostnameReissueCooldown {
			continue
		}
		if hostnameDNSResolved(ctx, p.hostname, cfg.ClusterLBIP) {
			continue
		}
		if err := reissueDefaultDomainDNS(ctx, pool, p.projectID, p.environmentID, p.appName, p.hostname); err != nil {
			log.Warn().Err(err).Str("hostname", p.hostname).Msg("failed to re-issue DNS write for stuck managed hostname")
			continue
		}
		log.Warn().
			Str("hostname", p.hostname).
			Dur("pending_for", time.Since(p.createdAt)).
			Msg("managed hostname A record unresolved past window -- re-issued DNS write")
	}
	return nil
}

// hostnameDNSResolved reports whether hostname's A record currently resolves
// to target. Used to distinguish "DNS write landed, cert issuance is just
// still in flight" from "the DNS write was never published (or was lost)" for
// managed hostnames, which is the case ReconcilePendingHostnames can recover
// from by re-issuing the write.
func hostnameDNSResolved(parent context.Context, hostname, target string) bool {
	ctx, cancel := context.WithTimeout(parent, hostnameDNSLookupTimeout)
	defer cancel()
	var resolver net.Resolver
	ips, err := resolver.LookupIP(ctx, "ip4", hostname)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if ip.String() == target {
			return true
		}
	}
	return false
}

// reissueDefaultDomainDNS re-drives the DNS write for a managed hostname whose
// A record never landed. It enqueues the same AttachDefaultDomain operation
// gitops-agent's doAttachDefaultDomain already handles to backfill a surrogate
// domain onto an existing app: a fresh operation id re-renders the hostname's
// Ingress and DNS-only PublicApi composite into git with a new
// dada.io/operation label, producing a real commit so Argo/Crossplane observe
// drift and re-attempt the Beget changeRecords call. The existing pending
// domain_hostnames row is left as-is; only last_reissue_at is bumped, so the
// next reconcile tick's cooldown check prevents re-issuing again before
// hostnameReissueCooldown elapses.
func reissueDefaultDomainDNS(ctx context.Context, pool *pgxpool.Pool, projectID, environmentID uuid.UUID, appName, hostname string) error {
	payload, err := json.Marshal(models.AttachCustomHostnamePayload{AppName: appName, Hostname: hostname})
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'AttachDefaultDomain', 'App', $4, 'Created', $5)`,
		reissueActorID, projectID, environmentID, appName, payload,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE domain_hostnames SET last_reissue_at = now() WHERE hostname = $1 AND status = 'pending'`,
		hostname,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// defaultDomainBackfillGrace is how long a freshly-touched App snapshot is left
// alone before BackfillMissingDefaultDomains will consider its missing
// domain_hostnames row abandoned rather than "CreateApp is still mid-flight".
// CreateApp writes the App snapshot and the domain_hostnames row from the same
// request handler, so under normal operation the row appears within
// milliseconds; this window only needs to clear worst-case request latency,
// not gitops-agent's async git commit (that commit is what actually renders
// the Ingress/DNS files and can legitimately take longer, but it never removes
// the domain_hostnames row CreateApp already inserted).
const defaultDomainBackfillGrace = 5 * time.Minute

// appNeedsDefaultDomain reports whether an App resource_snapshot's summary_json
// describes an HTTP-serving app that CreateApp would have assigned a default
// hostname to. Requires an explicit positive port: every app created through
// CreateApp records its port in the snapshot summary, while snapshots synced
// from hand-maintained gitops trees (platform/example/legacy projects) carry
// no port at all. Falling back to a framework-default port here once matched
// 13 platform infra apps (opensearch, keycloak-config, ...) and rendered
// public Ingress manifests for them, so portless snapshots are excluded
// outright rather than guessed at.
//
// A worker app (summary["worker"] == true) is excluded regardless of port: it
// is a long-poll/background process (e.g. a Telegram bot) with no HTTP server
// to answer a public request, so an auto domain would only ever 502. CreateApp
// stamps "worker" into the snapshot gitops-agent's doCreateApp writes (see
// dbwatcher.go), and every later live-status update merges into that
// summary_json rather than replacing it, so the flag survives for the
// lifetime of the app.
func appNeedsDefaultDomain(summary map[string]any) bool {
	if worker, _ := summary["worker"].(bool); worker {
		return false
	}
	port, _ := summary["port"].(float64)
	if port <= 0 {
		return false
	}
	return servesHTTP(int(port))
}

// BackfillMissingDefaultDomains finds HTTP-serving Kubernetes apps that have no
// domain_hostnames row at all and re-drives default-domain provisioning for
// them via the same AttachDefaultDomain operation the manual re-attach path
// uses (doAttachDefaultDomain in gitops-agent is idempotent: it upserts the
// Ingress/DNS manifest block, so running it against an app that already has
// working ingress files is harmless drift-correction, not a duplicate).
//
// This closes the one-time-provisioning gap in CreateApp: if the domain step
// fails to leave a row behind -- the CreateApp request's own INSERT failing
// after the operation already committed (see the best-effort insert in
// CreateApp), or an app being recovered by hand straight into git without
// going through the API -- nothing else ever notices, since
// ReconcilePendingHostnames only walks EXISTING domain_hostnames rows. A
// missing row therefore stayed domain-less forever until this ran.
func BackfillMissingDefaultDomains(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config) error {
	if cfg == nil || !cfg.DefaultDomainEnabled || cfg.DefaultDomainBase == "" {
		return nil
	}
	rows, err := pool.Query(ctx,
		`SELECT rs.project_id, rs.environment_id, rs.name, rs.summary_json
		   FROM resource_snapshots rs
		   JOIN environments e ON e.id = rs.environment_id
		  WHERE rs.kind = 'App'
		    AND rs.environment_id IS NOT NULL
		    AND e.runtime = $1
		    AND rs.last_synced_at < NOW() - ($2 * INTERVAL '1 second')
		    AND NOT EXISTS (
		        SELECT 1 FROM domain_hostnames dh
		         WHERE dh.environment_id = rs.environment_id AND dh.app_name = rs.name
		    )`,
		models.EnvironmentRuntimeK8s, defaultDomainBackfillGrace.Seconds(),
	)
	if err != nil {
		return err
	}
	type appRow struct {
		projectID     uuid.UUID
		environmentID uuid.UUID
		name          string
		summaryRaw    []byte
	}
	var apps []appRow
	for rows.Next() {
		var a appRow
		if err := rows.Scan(&a.projectID, &a.environmentID, &a.name, &a.summaryRaw); err != nil {
			rows.Close()
			return err
		}
		apps = append(apps, a)
	}
	rows.Close()

	for _, a := range apps {
		var summary map[string]any
		_ = json.Unmarshal(a.summaryRaw, &summary)
		if !appNeedsDefaultDomain(summary) {
			continue
		}
		suffix, sErr := randomHostSuffix()
		if sErr != nil {
			log.Warn().Err(sErr).Str("app", a.name).Msg("backfill default domain: suffix generation failed")
			continue
		}
		hostname := buildDefaultHostname(cfg.DefaultDomainBase, a.name, suffix)
		if err := enqueueDefaultDomainBackfill(ctx, pool, a.projectID, a.environmentID, a.name, hostname); err != nil {
			log.Warn().Err(err).Str("app", a.name).Str("hostname", hostname).
				Msg("backfill default domain: enqueue failed")
			continue
		}
		log.Warn().Str("app", a.name).Str("hostname", hostname).
			Msg("backfilled missing default domain for app with no domain_hostnames row")
	}
	return nil
}

// enqueueDefaultDomainBackfill inserts the domain_hostnames row CreateApp would
// normally have inserted plus the AttachDefaultDomain operation that renders it,
// in one transaction so the row and its provisioning operation never diverge.
func enqueueDefaultDomainBackfill(ctx context.Context, pool *pgxpool.Pool, projectID, environmentID uuid.UUID, appName, hostname string) error {
	payload, err := json.Marshal(models.AttachCustomHostnamePayload{AppName: appName, Hostname: hostname})
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var opID uuid.UUID
	if err := tx.QueryRow(ctx,
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, 'AttachDefaultDomain', 'App', $4, 'Created', $5)
		 RETURNING id`,
		reissueActorID, projectID, environmentID, appName, payload,
	).Scan(&opID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO domain_hostnames (authorization_id, environment_id, app_name, hostname, record_type, status, cert_status, operation_id, managed)
		 VALUES (NULL, $1, $2, $3, 'CNAME', 'pending', 'pending', $4, true)`,
		environmentID, appName, hostname, opID,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// hostnameCertLive reports whether hostname:443 completes a TLS handshake with a
// publicly-trusted certificate valid for that hostname. The default (verifying)
// tls.Config means a self-signed / ingress-default / expired cert fails the
// handshake, so only a genuinely issued LE cert returns true.
func hostnameCertLive(ctx context.Context, hostname string) bool {
	dctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	dialer := tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 5 * time.Second},
		Config:    &tls.Config{ServerName: hostname},
	}
	conn, err := dialer.DialContext(dctx, "tcp", hostname+":443")
	if err != nil {
		return false
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return false
	}
	return state.PeerCertificates[0].VerifyHostname(hostname) == nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomHostSuffix() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func buildDefaultHostname(base, name, suffix string) string {
	label := name
	maxLabel := 63 - 1 - len(suffix)
	if len(label) > maxLabel {
		label = strings.TrimRight(label[:maxLabel], "-")
	}
	return fmt.Sprintf("%s-%s.%s", label, suffix, base)
}

// normalizeDomain lowercases and trims a domain, stripping any trailing dot and
// a leading wildcard label (wildcards are out of MVP scope).
func normalizeDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimSuffix(d, ".")
	return d
}

// isValidDomain is a cheap structural check: at least two dot-separated labels,
// each 1-63 chars of [a-z0-9-], no leading/trailing hyphen.
func isValidDomain(d string) bool {
	if len(d) == 0 || len(d) > 253 || strings.Contains(d, "*") {
		return false
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if len(l) == 0 || len(l) > 63 {
			return false
		}
		if l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := 0; i < len(l); i++ {
			ch := l[i]
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
		}
	}
	return true
}

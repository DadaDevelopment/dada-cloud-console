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

// ReconcilePendingHostnames flips a custom hostname from pending to active once
// its Let's Encrypt certificate is serving end-to-end, and fails hostnames that
// have been pending past hostnamePendingFailAfter. Nothing else updates the row
// after AttachHostname commits the Ingress to git, so without this a fully
// working domain shows "pending" forever in the console. The probe is an
// external TLS handshake to hostname:443 with SNI: it only succeeds when the
// leaf cert is publicly trusted (LE issued) and valid for the hostname, which
// proves DNS -> ingress -> cert all resolved. A failed probe within the window
// leaves the row pending to be retried on the next tick; past the window the row
// is marked failed. Both UPDATEs are guarded on status='pending' so a concurrent
// detach or a row that just went active is never clobbered.
func ReconcilePendingHostnames(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx,
		`SELECT id, hostname, created_at FROM domain_hostnames WHERE status = 'pending'`)
	if err != nil {
		return err
	}
	type pendingHost struct {
		id        uuid.UUID
		hostname  string
		createdAt time.Time
	}
	var pending []pendingHost
	for rows.Next() {
		var p pendingHost
		if err := rows.Scan(&p.id, &p.hostname, &p.createdAt); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, p)
	}
	rows.Close()

	for _, p := range pending {
		if hostnameCertLive(ctx, p.hostname) {
			_, _ = pool.Exec(ctx,
				`UPDATE domain_hostnames SET status='active', cert_status='active', updated_at=now()
				  WHERE id=$1 AND status='pending'`, p.id)
			continue
		}
		if hostnamePendingExpired(p.createdAt, time.Now()) {
			ct, err := pool.Exec(ctx,
				`UPDATE domain_hostnames SET status='failed', cert_status='failed', updated_at=now()
				  WHERE id=$1 AND status='pending'`, p.id)
			if err == nil && ct.RowsAffected() > 0 {
				log.Warn().
					Str("hostname", p.hostname).
					Dur("pending_for", time.Since(p.createdAt)).
					Msg("custom hostname failed: pending past attach window (app retired or Ingress missing) — re-attach to retry")
			}
		}
	}
	return nil
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

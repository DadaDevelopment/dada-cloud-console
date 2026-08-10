package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
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

	apex := ""
	rejectAuth := func(status int, reason, msg string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:    projectID,
			Action:       "AddDomainAuthorization",
			ResourceKind: "CustomDomain",
			ResourceName: apex,
			Outcome:      auditOutcomeFailure,
			Metadata:     map[string]any{"reason": reason, "status": status},
		})
		respondError(c, status, msg)
	}

	if orgID, orgErr := h.projectOrg(c.Request.Context(), projectID); orgErr == nil {
		if qErr := h.checkQuota(c.Request.Context(), orgID, "domains"); qErr != nil {
			if meta, blocked := billingBlockAudit(qErr); blocked {
				h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
					ProjectID:    projectID,
					Action:       "AddDomainAuthorization",
					ResourceKind: "CustomDomain",
					Outcome:      auditOutcomeFailure,
					Metadata:     meta,
				})
				h.respondBillingBlocked(c, orgID, qErr)
				return
			}
		}
	}

	var req addDomainAuthorizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		rejectAuth(http.StatusBadRequest, "malformed_body", err.Error())
		return
	}
	apex = normalizeDomain(req.ApexDomain)
	if !isValidDomain(apex) {
		rejectAuth(http.StatusBadRequest, "invalid_domain", "apex_domain must be a valid domain name")
		return
	}

	token, err := randomToken()
	if err != nil {
		rejectAuth(http.StatusInternalServerError, "token_generation_failed", "failed to generate token")
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
		rejectAuth(http.StatusConflict, "domain_taken", "that apex domain is already authorized by a project")
		return
	}
	if err != nil {
		rejectAuth(http.StatusInternalServerError, "create_failed", "failed to create authorization")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:    projectID,
		Action:       "AddDomainAuthorization",
		ResourceKind: "CustomDomain",
		ResourceName: apex,
		Metadata:     map[string]any{"authorization_id": a.ID.String(), "status": a.Status},
	})
	h.notifyAuditEvent(claims, projectID, "AddDomainAuthorization", apex)

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

	verifyOutcome := auditOutcomeFailure
	if a.Status == "verified" {
		verifyOutcome = auditOutcomeSuccess
	}
	verifyMeta := map[string]any{"status": a.Status}
	if a.ErrorMessage != "" {
		verifyMeta["reason"] = a.ErrorMessage
	}
	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:    projectID,
		Action:       "VerifyDomainAuthorization",
		ResourceKind: "CustomDomain",
		ResourceName: a.ApexDomain,
		Outcome:      verifyOutcome,
		Metadata:     verifyMeta,
	})

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
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: a.envID,
			OperationID:   opID,
			Action:        "DetachCustomHostname",
			ResourceKind:  "CustomDomain",
			ResourceName:  a.host,
			Metadata:      map[string]any{"app_name": a.appName, "cascade": "authorization_deleted"},
		})
	}

	var deletedApex string
	err = h.pool.QueryRow(c.Request.Context(),
		`DELETE FROM domain_authorizations WHERE id = $1 AND project_id = $2 RETURNING apex_domain`,
		authID, projectID).Scan(&deletedApex)
	if err == pgx.ErrNoRows {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:    projectID,
			Action:       "DeleteDomainAuthorization",
			ResourceKind: "CustomDomain",
			Outcome:      auditOutcomeFailure,
			Metadata:     map[string]any{"reason": "not_found", "status": http.StatusNotFound},
		})
		respondNotFound(c)
		return
	}
	if err != nil {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:    projectID,
			Action:       "DeleteDomainAuthorization",
			ResourceKind: "CustomDomain",
			Outcome:      auditOutcomeFailure,
			Metadata:     map[string]any{"reason": "delete_failed", "status": http.StatusInternalServerError},
		})
		respondError(c, http.StatusInternalServerError, "failed to delete authorization")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:    projectID,
		Action:       "DeleteDomainAuthorization",
		ResourceKind: "CustomDomain",
		ResourceName: deletedApex,
		Metadata:     map[string]any{"detached_hostnames": len(attached)},
	})
	h.notifyAuditEvent(claims, projectID, "DeleteDomainAuthorization", deletedApex)

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

	hostname := ""
	rejectAttach := func(status int, reason, msg string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "AttachCustomHostname",
			ResourceKind:  "CustomDomain",
			ResourceName:  hostname,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status, "app_name": appName},
		})
		respondError(c, status, msg)
	}

	var req attachHostnameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		rejectAttach(http.StatusBadRequest, "malformed_body", err.Error())
		return
	}
	hostname = normalizeDomain(req.Hostname)
	if !isValidDomain(hostname) {
		rejectAttach(http.StatusBadRequest, "invalid_hostname", "hostname must be a valid domain name")
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
		rejectAttach(http.StatusForbidden, "no_verified_apex", "no verified apex authorization covers this hostname")
		return
	}
	if err != nil {
		rejectAttach(http.StatusInternalServerError, "authorization_lookup_failed", "failed to check authorization")
		return
	}

	// App must exist in this environment.
	var appCount int
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&appCount); err != nil {
		rejectAttach(http.StatusInternalServerError, "app_lookup_failed", "failed to verify app")
		return
	}
	if appCount == 0 {
		rejectAttach(http.StatusNotFound, "app_not_found", "app not found")
		return
	}

	recordType := "CNAME"
	if hostname == apex {
		recordType = "A"
	}

	dnsTarget := h.recordTarget(recordType)
	dnsNote := ""
	var envRuntime models.EnvironmentRuntime
	var appServerID *uuid.UUID
	if err := h.pool.QueryRow(c.Request.Context(),
		`SELECT runtime, app_server_id FROM environments WHERE id = $1 AND project_id = $2`,
		envID, projectID,
	).Scan(&envRuntime, &appServerID); err == nil && envRuntime == models.EnvironmentRuntimeVM {
		recordType = "A"
		dnsTarget = ""
		dnsNote = "no VM host assigned to this environment yet; DNS target pending"
		if appServerID != nil {
			var vmIP *string
			if err := h.pool.QueryRow(c.Request.Context(),
				`SELECT vm_ip FROM app_servers WHERE id = $1`, *appServerID,
			).Scan(&vmIP); err == nil && vmIP != nil && *vmIP != "" {
				dnsTarget = *vmIP
				dnsNote = ""
			} else {
				dnsNote = "VM host has no public IP recorded yet; DNS target pending"
			}
		}
	}

	payload := models.AttachCustomHostnamePayload{AppName: appName, Hostname: hostname}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		rejectAttach(http.StatusInternalServerError, "marshal_failed", "failed to marshal payload")
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
		rejectAttach(http.StatusInternalServerError, "operation_create_failed", "failed to create operation")
		return
	}

	var hn models.DomainHostname
	err = h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO domain_hostnames (authorization_id, environment_id, app_name, hostname, record_type, operation_id)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, authorization_id, environment_id, app_name, hostname, record_type,
		           status, cert_status, status_reason, operation_id, created_at, updated_at`,
		authID, envID, appName, hostname, recordType, op.ID,
	).Scan(
		&hn.ID, &hn.AuthorizationID, &hn.EnvironmentID, &hn.AppName, &hn.Hostname, &hn.RecordType,
		&hn.Status, &hn.CertStatus, &hn.StatusReason, &hn.OperationID, &hn.CreatedAt, &hn.UpdatedAt,
	)
	if isUniqueViolation(err) {
		rejectAttach(http.StatusConflict, "hostname_already_attached", "that hostname is already attached")
		return
	}
	if err != nil {
		rejectAttach(http.StatusInternalServerError, "record_failed", "failed to record hostname")
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "AttachCustomHostname",
		ResourceKind:  "CustomDomain",
		ResourceName:  hostname,
		Metadata:      map[string]any{"app_name": appName, "record_type": recordType, "apex": apex},
	})
	h.notifyAuditEvent(claims, projectID, "AttachCustomHostname", hostname)

	dnsRecord := gin.H{
		"type":   recordType,
		"host":   hostname,
		"target": dnsTarget,
	}
	if dnsNote != "" {
		dnsRecord["note"] = dnsNote
	}

	c.JSON(http.StatusAccepted, gin.H{
		"operation":  op,
		"hostname":   hn,
		"dns_record": dnsRecord,
		"message":    "Hostname attachment queued",
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
		        status, cert_status, status_reason, operation_id, created_at, updated_at
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
			&hn.Status, &hn.CertStatus, &hn.StatusReason, &hn.OperationID, &hn.CreatedAt, &hn.UpdatedAt,
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

	detachHostname := ""
	rejectDetach := func(status int, reason, msg string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "DetachCustomHostname",
			ResourceKind:  "CustomDomain",
			ResourceName:  detachHostname,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status, "app_name": appName},
		})
		respondError(c, status, msg)
	}

	if ok, err := h.envBelongsToProject(c.Request.Context(), envID, projectID); err != nil {
		rejectDetach(http.StatusInternalServerError, "env_lookup_failed", "failed to verify environment")
		return
	} else if !ok {
		rejectDetach(http.StatusNotFound, "env_not_in_project", "not found")
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
		rejectDetach(http.StatusNotFound, "hostname_not_found", "not found")
		return
	}
	if err != nil {
		rejectDetach(http.StatusInternalServerError, "hostname_load_failed", "failed to load hostname")
		return
	}
	detachHostname = hn.Hostname
	if hn.Managed && appStillNeedsDefaultDomain(c.Request.Context(), h.pool, envID, appName) {
		rejectDetach(http.StatusConflict, "managed_domain", "the default domain cannot be detached")
		return
	}

	payload := models.DetachCustomHostnamePayload{AppName: appName, Hostname: hn.Hostname}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		rejectDetach(http.StatusInternalServerError, "marshal_failed", "failed to marshal payload")
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
		rejectDetach(http.StatusInternalServerError, "operation_create_failed", "failed to create operation")
		return
	}

	// Drop the hostname record now that teardown is queued.
	_, _ = h.pool.Exec(c.Request.Context(), `DELETE FROM domain_hostnames WHERE id = $1`, hostnameID)

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		OperationID:   op.ID,
		Action:        "DetachCustomHostname",
		ResourceKind:  "CustomDomain",
		ResourceName:  hn.Hostname,
		Metadata:      map[string]any{"app_name": appName, "record_type": hn.RecordType},
	})
	h.notifyAuditEvent(claims, projectID, "DetachCustomHostname", hn.Hostname)

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

// hostnameReattachCooldown bounds how often ReattachOrphanedHostnames may
// re-drive a single failed row. The class of row this pass targets got
// orphaned by a config error outside its control (DeleteApp removing the
// Ingress, CreateApp under the same name never restoring it), and the same
// query re-runs on every domain-maintenance tick -- without a per-row
// cooldown a row that keeps re-failing for some other reason (e.g. the app
// itself is unhealthy) would be re-driven every tick, hammering gitops with
// operations for a domain that cannot actually come up. Six hours is long
// enough that a spurious re-drive costs nothing meaningful, short enough that
// a real fix (app comes back healthy) is picked up the same day.
const hostnameReattachCooldown = 6 * time.Hour

// hostnameReattachMaxAttempts caps how many times ReattachOrphanedHostnames
// will re-drive the same row before giving up on it permanently. This is the
// project's standing rule after the 56.5k-generation DNS self-feeding storm:
// any background loop that re-drives its own failure state must have a hard
// ceiling, not just a cooldown, or a row stuck for a reason the pass cannot
// fix (app permanently misconfigured, hostname genuinely dead) retries
// forever at a slower cadence instead of stopping. Three tries spaced by
// hostnameReattachCooldown is 18 hours of chances -- past that the row stays
// failed and the owner has to re-attach it by hand, which is also the
// correct outcome for a hostname that is not this pass's job to save.
const hostnameReattachMaxAttempts = 3

// hostnameDNSLookupTimeout bounds each verify-resolve check.
const hostnameDNSLookupTimeout = 3 * time.Second

// hostnameReasonDNSNotPointed is the status_reason (migration 084) for a
// hostname whose public DNS does not resolve to the address we told the user to
// point it at. Nothing on our side can progress while that is true -- ACME's
// HTTP-01 challenge is answered by whoever the record still points at -- so
// this is the one pending state where the user, not the platform, is the
// blocker, and saying so is the whole point of the column. The value is a
// machine code the console translates, never prose.
const hostnameReasonDNSNotPointed = "dns_not_pointed"

// hostnameReasonCertPending is the status_reason for a hostname whose DNS
// already points at us and whose certificate is simply not being served yet:
// ordinary in-flight issuance, no user action.
const hostnameReasonCertPending = "cert_pending"

// hostnameReasonAttachTimeout is the status_reason left on a hostname failed
// for never serving its certificate within hostnamePendingFailAfter.
const hostnameReasonAttachTimeout = "attach_timeout"

// hostnameReasonRouteMissing is the status_reason for a hostname whose TLS
// handshake succeeds (the wildcard cert covering our managed *.dada-tuda.ru
// surrogates always answers, regardless of whether anything routes the name)
// but has no Ingress rule for it in the cluster. Without this check a
// surrogate hostname with no live Ingress -- app deleted, Ingress dropped from
// git out-of-band -- passed hostnameCertLive on the wildcard alone and showed
// green in the console over a dead URL. Route existence is the second half of
// "active": a live cert and a live route to serve it.
const hostnameReasonRouteMissing = "route_missing"

// hostnameReasonAppDeleted is the status_reason DeleteApp stamps on a
// domain_hostnames row it demotes from 'active'/'pending' to 'failed'
// (delete_impact.go, demoteAppHostnames) so ReattachOrphanedHostnames can
// tell "the app underneath this row was deleted" apart from every other
// reason a hostname failed.
const hostnameReasonAppDeleted = "app_deleted"

// domainRouteClientsetFactory builds the kube client the route checks dial,
// indirected through a var (rather than calling newAppHealthClientset
// directly) so tests can swap in a fake clientset and exercise the
// cert+route decision logic without an in-cluster service account.
var domainRouteClientsetFactory = newAppHealthClientset

// reissueActorID is the fixed system-user id (see migration 010_system_user.sql)
// used as actor_id for operations the reconciler enqueues on its own, with no
// human actor behind them.
var reissueActorID = uuid.MustParse("00000000-0000-0000-0000-000000000000")

// ReconcilePendingHostnames flips a hostname from pending to active once its
// Let's Encrypt certificate is serving end-to-end AND (for non-VM runtimes) an
// Ingress rule for it actually exists in the cluster, fails hostnames that
// have been pending past hostnamePendingFailAfter, and -- for managed
// (surrogate) hostnames only -- re-issues the DNS write when the A record
// itself never resolved within hostnameDNSStuckAfter. Nothing else updates
// the row after AttachHostname/CreateApp commits the Ingress (and, for
// managed rows, the PublicApi DNS composite) to git, so without this a fully
// working domain shows "pending" forever in the console, and a managed
// hostname whose DNS write was dropped (e.g. a Beget-API egress block at
// write time) stays NXDOMAIN forever with no auto-recovery.
//
// The route check exists because our managed surrogate hostnames
// (*.dada-tuda.ru) share one wildcard certificate: hostnameCertLive alone
// passes for ANY name under that wildcard whether or not anything in the
// cluster actually routes it, which is exactly how 14 dead surrogate rows
// went active with no Ingress behind them. A cert-only check cannot tell
// "served by us" from "matches our wildcard", so the route must be verified
// independently via the kube API.
//
// The cert probe is a TLS handshake with SNI set to the hostname, aimed at our
// OWN ingress (cfg.IngressTLSProbeAddr), not at the hostname's public address.
// Aiming it at the public address is what this function used to do and it is
// unsound in exactly the case custom domains exist for: a domain being migrated
// from another provider still answers its own name with a valid publicly
// trusted certificate, so the probe passed seconds after attach and the console
// showed a green "active" domain that served the OLD host. Dialling our ingress
// instead proves the certificate is issued AND being served by us, which is the
// only thing "active" can honestly mean. A failed probe within the attach
// window leaves the row pending with a status_reason to be retried on the next
// tick; past the window the row is marked failed. Every UPDATE is guarded on
// status='pending' so a concurrent detach or a row that just went active is
// never clobbered.
func ReconcilePendingHostnames(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config) error {
	clientset := domainRouteClientsetFactory()
	rows, err := pool.Query(ctx,
		`SELECT dh.id, dh.hostname, COALESCE(dh.attach_started_at, dh.created_at), dh.managed, dh.environment_id, dh.app_name,
		        dh.last_reissue_at, dh.status_reason, e.project_id, e.runtime, a.vm_ip
		   FROM domain_hostnames dh
		   JOIN environments e ON e.id = dh.environment_id
		   LEFT JOIN app_servers a ON a.id = e.app_server_id
		  WHERE dh.status = 'pending'`)
	if err != nil {
		return err
	}
	type pendingHost struct {
		id              uuid.UUID
		hostname        string
		attachStartedAt time.Time
		managed         bool
		environmentID   uuid.UUID
		appName         string
		lastReissue     *time.Time
		statusReason    *string
		projectID       uuid.UUID
		runtime         models.EnvironmentRuntime
		vmIP            *string
	}
	var pending []pendingHost
	for rows.Next() {
		var p pendingHost
		if err := rows.Scan(&p.id, &p.hostname, &p.attachStartedAt, &p.managed, &p.environmentID,
			&p.appName, &p.lastReissue, &p.statusReason, &p.projectID, &p.runtime, &p.vmIP); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, p)
	}
	rows.Close()

	now := time.Now()
	for _, p := range pending {
		probeAddr, dnsTarget, probeable := hostnameProbeTargets(cfg, p.hostname, p.runtime, p.vmIP)
		if !probeable {
			continue
		}
		if hostnameCertLive(ctx, p.hostname, probeAddr) {
			routeLive, routeKnown := true, true
			if p.runtime != models.EnvironmentRuntimeVM {
				routeLive, routeKnown = hostnameRouteLive(ctx, clientset, p.hostname)
			}
			switch hostnameCertRouteDecision(p.runtime, routeKnown, routeLive) {
			case hostnameOutcomeUnknown:
			case hostnameOutcomeRouteMissing:
				if p.statusReason == nil || *p.statusReason != hostnameReasonRouteMissing {
					_, _ = pool.Exec(ctx,
						`UPDATE domain_hostnames SET status_reason=$2, updated_at=now()
						  WHERE id=$1 AND status='pending'`, p.id, hostnameReasonRouteMissing)
				}
			case hostnameOutcomeActive:
				_, _ = pool.Exec(ctx,
					`UPDATE domain_hostnames SET status='active', cert_status='active', status_reason=NULL, updated_at=now()
					  WHERE id=$1 AND status='pending'`, p.id)
			}
			continue
		}
		reason := hostnameReasonCertPending
		if dnsTarget != "" && !hostnameDNSResolved(ctx, p.hostname, dnsTarget) {
			reason = hostnameReasonDNSNotPointed
		}
		if hostnamePendingExpired(p.attachStartedAt, now) {
			ct, err := pool.Exec(ctx,
				`UPDATE domain_hostnames SET status='failed', cert_status='failed', status_reason=$2, updated_at=now()
				  WHERE id=$1 AND status='pending'`, p.id, hostnameReasonAttachTimeout)
			if err == nil && ct.RowsAffected() > 0 {
				log.Warn().
					Str("hostname", p.hostname).
					Str("last_reason", reason).
					Dur("pending_for", time.Since(p.attachStartedAt)).
					Msg("hostname failed: pending past attach window (DNS never moved, app retired or Ingress missing) -- re-attach to retry")
			}
			continue
		}
		if p.statusReason == nil || *p.statusReason != reason {
			_, _ = pool.Exec(ctx,
				`UPDATE domain_hostnames SET status_reason=$2, updated_at=now()
				  WHERE id=$1 AND status='pending'`, p.id, reason)
		}
		if !p.managed || cfg == nil || cfg.ClusterLBIP == "" {
			continue
		}
		if now.Sub(p.attachStartedAt) <= hostnameDNSStuckAfter {
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
			Dur("pending_for", time.Since(p.attachStartedAt)).
			Msg("managed hostname A record unresolved past window -- re-issued DNS write")
	}
	return nil
}

// RevalidateActiveHostnameRoutes catches the other side of the route_missing
// bug: ReconcilePendingHostnames only ever looks at status='pending' rows, so
// a hostname that already flipped to 'active' before this check existed (or
// whose Ingress was deleted from git after going active -- app retired,
// hand-written "clean up" commit) stays green forever with nothing that ever
// re-examines it. This walks the active rows instead and, for k8s runtimes
// only, sends them back to pending with status_reason=route_missing the
// moment their Ingress rule disappears. It deliberately does NOT touch the
// certificate: hostnameCertLive already passed once to get here, and a
// transient probe failure is not this function's job to chase.
//
// A kube-API error or timeout on hostnameRouteLive means "unknown", not
// "absent" -- the row is left untouched rather than demoted, so a control-plane
// blip never flaps an otherwise-healthy domain back to pending.
func RevalidateActiveHostnameRoutes(ctx context.Context, pool *pgxpool.Pool) error {
	clientset := domainRouteClientsetFactory()
	if clientset == nil {
		return nil
	}
	rows, err := pool.Query(ctx,
		`SELECT dh.id, dh.hostname, e.runtime
		   FROM domain_hostnames dh
		   JOIN environments e ON e.id = dh.environment_id
		  WHERE dh.status = 'active'`)
	if err != nil {
		return err
	}
	type activeHost struct {
		id       uuid.UUID
		hostname string
		runtime  models.EnvironmentRuntime
	}
	var active []activeHost
	for rows.Next() {
		var a activeHost
		if err := rows.Scan(&a.id, &a.hostname, &a.runtime); err != nil {
			rows.Close()
			return err
		}
		active = append(active, a)
	}
	rows.Close()

	for _, a := range active {
		if a.runtime == models.EnvironmentRuntimeVM {
			continue
		}
		routeLive, known := hostnameRouteLive(ctx, clientset, a.hostname)
		if !known || routeLive {
			continue
		}
		if _, err := pool.Exec(ctx,
			`UPDATE domain_hostnames SET status='pending', status_reason=$2, updated_at=now()
			  WHERE id=$1 AND status='active'`, a.id, hostnameReasonRouteMissing,
		); err == nil {
			log.Warn().
				Str("hostname", a.hostname).
				Msg("active hostname has no matching Ingress route -- reverted to pending")
		}
	}
	return nil
}

// hostnameCertRouteOutcome is what ReconcilePendingHostnames does with a
// pending hostname whose cert probe already passed, once the route check
// weighs in.
type hostnameCertRouteOutcome int

const (
	// hostnameOutcomeActive: VM runtime (no route check applies), or a route
	// check that confirmed the Ingress rule exists.
	hostnameOutcomeActive hostnameCertRouteOutcome = iota
	// hostnameOutcomeRouteMissing: the route check ran and confirmed there is
	// no Ingress rule for the hostname -- the row stays pending with the
	// route_missing reason.
	hostnameOutcomeRouteMissing
	// hostnameOutcomeUnknown: the route check could not run (no in-cluster
	// client) or errored (kube-API timeout/failure) -- the row is left
	// exactly as it was, to be retried next tick.
	hostnameOutcomeUnknown
)

// hostnameCertRouteDecision turns a route lookup into the action
// ReconcilePendingHostnames takes for a hostname whose cert probe already
// succeeded. Kept separate from the lookup itself (hostnameRouteLive, which
// needs a live or fake kube client) so the actual branching -- the part a
// wrong kube-API response must not silently corrupt into a flap -- is
// testable with plain booleans.
func hostnameCertRouteDecision(runtime models.EnvironmentRuntime, routeKnown, routeLive bool) hostnameCertRouteOutcome {
	if runtime == models.EnvironmentRuntimeVM {
		return hostnameOutcomeActive
	}
	if !routeKnown {
		return hostnameOutcomeUnknown
	}
	if !routeLive {
		return hostnameOutcomeRouteMissing
	}
	return hostnameOutcomeActive
}

// hostnameRouteLive reports whether the cluster has an Ingress with a rule for
// hostname. known is false when the answer cannot be determined -- no
// in-cluster kube client (local dev, off-cluster tests) or the list call
// itself failed/timed out -- so callers can treat "unknown" as "leave the row
// alone" rather than as "the route is gone", which is the difference between a
// genuine dead domain and a momentary API-server hiccup.
//
// The list is cluster-wide on purpose. A hostname is not always routed from
// the environment's own namespace: PR previews under *.pv.dada-tuda.ru are
// served by one shared wildcard Ingress living in argocd-prod, so a
// namespace-scoped lookup would report every live preview domain as
// route-missing and flap it back to pending.
func hostnameRouteLive(parent context.Context, clientset kubernetes.Interface, hostname string) (live bool, known bool) {
	if clientset == nil {
		return false, false
	}
	ctx, cancel := context.WithTimeout(parent, 6*time.Second)
	defer cancel()
	list, err := clientset.NetworkingV1().Ingresses(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, false
	}
	for _, ing := range list.Items {
		if ingressRouteMatchesHost(ing.Spec.Rules, hostname) {
			return true, true
		}
	}
	return false, true
}

// ingressRouteMatchesHost reports whether any rule in rules routes hostname,
// counting the single-label wildcard form Kubernetes itself defines
// (*.example.com matches foo.example.com but never bar.foo.example.com or the
// bare apex). Pulled out of hostnameRouteLive so the matching logic itself --
// the part worth getting right -- is testable without a fake clientset.
func ingressRouteMatchesHost(rules []networkingv1.IngressRule, hostname string) bool {
	for _, rule := range rules {
		if rule.Host == hostname {
			return true
		}
		suffix, ok := strings.CutPrefix(rule.Host, "*.")
		if !ok {
			continue
		}
		label, rest, found := strings.Cut(hostname, ".")
		if found && label != "" && rest == suffix {
			return true
		}
	}
	return false
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

// defaultDomainBackfillGrace is how long a freshly-CREATED App snapshot is left
// alone before BackfillMissingDefaultDomains will consider its missing
// domain_hostnames row abandoned rather than "CreateApp is still mid-flight".
// It is measured against resource_snapshots.first_seen_at, the app's age.
// Measuring it against last_synced_at inverts the filter: the snapshot sync
// bumps last_synced_at every tick, so on prod 62 of 81 App snapshots are under
// a minute old at any moment and those are precisely the LIVE apps -- such a
// window would admit only stale, i.e. abandoned, snapshots and never fix the
// app the pass exists for.
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
// appStillNeedsDefaultDomain gates DetachHostname's managed-domain refusal: an
// ordinary HTTP app must keep its only public route, but an app retrofitted
// as a worker (or left with no configured port) after already receiving a
// default domain -- the exact class of already-affected app this whole flow
// exists to unstick -- has a route that only ever 502s, and its owner needs a
// way to remove it. Reuses appNeedsDefaultDomain's same worker/port judgment
// so the attach-time and detach-time answers can never disagree.
//
// Fails closed (still needs the domain, so detach stays refused) on any
// lookup problem -- missing snapshot, DB error -- since the alternative is
// silently letting a live HTTP app lose its only public route.
func appStillNeedsDefaultDomain(ctx context.Context, pool *pgxpool.Pool, environmentID uuid.UUID, appName string) bool {
	var summaryRaw []byte
	err := pool.QueryRow(ctx,
		`SELECT summary_json FROM resource_snapshots
		  WHERE environment_id = $1 AND kind = 'App' AND name = $2`,
		environmentID, appName,
	).Scan(&summaryRaw)
	if err != nil {
		return true
	}
	var summary map[string]any
	if err := json.Unmarshal(summaryRaw, &summary); err != nil {
		return true
	}
	return appNeedsDefaultDomain(summary)
}

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
		    AND `+notOrphanedSnapshot+`
		    AND rs.first_seen_at < NOW() - ($2 * INTERVAL '1 second')
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

// ReattachOrphanedHostnames finds domain_hostnames rows stuck in 'failed' whose
// owning app is demonstrably alive and re-drives provisioning for them.
//
// This closes a gap distinct from BackfillMissingDefaultDomains and
// reissueDefaultDomainDNS: those two recover a MISSING row and a managed row
// whose DNS write was lost, but neither ever looks at a row that already
// exists and already failed. That is exactly what DeleteApp followed by
// CreateApp under the same name produces -- DeleteApp removes the whole
// gitops-repo app directory (Ingress included), CreateApp restores the
// workload but never re-reads or re-renders the surviving domain_hostnames
// row, so the row sits there with a live app underneath it and no route to
// it, forever. Neither ReconcilePendingHostnames (only walks 'pending' rows)
// nor BackfillMissingDefaultDomains (its NOT EXISTS excludes any app that
// already has a row, failed or not) ever revisits it.
//
// The three preconditions -- a live App snapshot for the same environment
// and name, appNeedsDefaultDomain (excludes worker/portless apps, the same
// judgment CreateApp itself uses), and the grace window against a
// just-created app mid backfill -- are the same shape as
// BackfillMissingDefaultDomains for the same reasons. What is different here
// is the row already carries identity: hostname, record_type, and (for
// unmanaged rows) authorization_id are never touched, only status is driven
// back to pending. A managed (surrogate) row re-enqueues AttachDefaultDomain;
// an unmanaged (user custom-domain) row re-enqueues AttachCustomHostname, but
// ONLY when its authorization is still 'verified' -- an unmanaged row with no
// authorization or a non-verified one must not be silently re-attached, since
// that would (re-)issue a certificate for a domain nobody has proven
// ownership of at this moment.
func ReattachOrphanedHostnames(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config) error {
	if cfg == nil {
		return nil
	}
	rows, err := pool.Query(ctx,
		`SELECT dh.id, dh.hostname, dh.app_name, dh.environment_id, dh.managed, dh.authorization_id,
		        e.project_id, rs.summary_json, da.status
		   FROM domain_hostnames dh
		   JOIN environments e ON e.id = dh.environment_id
		   JOIN resource_snapshots rs
		     ON rs.environment_id = dh.environment_id AND rs.kind = 'App' AND rs.name = dh.app_name
		   LEFT JOIN domain_authorizations da ON da.id = dh.authorization_id
		  WHERE dh.status = 'failed'
		    AND e.runtime = $1
		    AND rs.first_seen_at < NOW() - ($2 * INTERVAL '1 second')
		    AND dh.reattach_count < $3
		    AND dh.updated_at < NOW() - ($4 * INTERVAL '1 second')`,
		models.EnvironmentRuntimeK8s, defaultDomainBackfillGrace.Seconds(),
		hostnameReattachMaxAttempts, hostnameReattachCooldown.Seconds(),
	)
	if err != nil {
		return err
	}
	type orphanRow struct {
		id            uuid.UUID
		hostname      string
		appName       string
		environmentID uuid.UUID
		managed       bool
		authID        *uuid.UUID
		projectID     uuid.UUID
		summaryRaw    []byte
		authStatus    *string
	}
	var orphans []orphanRow
	for rows.Next() {
		var o orphanRow
		if err := rows.Scan(&o.id, &o.hostname, &o.appName, &o.environmentID, &o.managed, &o.authID,
			&o.projectID, &o.summaryRaw, &o.authStatus); err != nil {
			rows.Close()
			return err
		}
		orphans = append(orphans, o)
	}
	rows.Close()

	for _, o := range orphans {
		var summary map[string]any
		_ = json.Unmarshal(o.summaryRaw, &summary)
		if !appNeedsDefaultDomain(summary) {
			continue
		}
		action := "AttachDefaultDomain"
		if !o.managed {
			if o.authID == nil || o.authStatus == nil || *o.authStatus != "verified" {
				continue
			}
			action = "AttachCustomHostname"
		}
		if err := reattachOrphanedHostname(ctx, pool, o.id, o.projectID, o.environmentID, o.appName, o.hostname, action); err != nil {
			log.Warn().Err(err).Str("hostname", o.hostname).Str("app", o.appName).
				Msg("reattach orphaned domain: failed to re-drive")
			continue
		}
		log.Warn().Str("hostname", o.hostname).Str("app", o.appName).Str("action", action).
			Msg("reattached orphaned domain: failed hostname re-driven for a live app")
	}
	return nil
}

// hostnameReapEnvFreshnessWindow bounds how old the newest live App
// resource_snapshot in an environment may be for ReapOrphanedAppHostnames to
// trust that environment's snapshot data at all. See ReapOrphanedAppHostnames
// for why this exists.
const hostnameReapEnvFreshnessWindow = 15 * time.Minute

// hostnameReapMaxRowsPerTick caps how many domain_hostnames rows
// ReapOrphanedAppHostnames may demote in a single tick, as a second,
// independent bound on the same failure mode hostnameReapEnvFreshnessWindow
// guards against: even if the freshness signal itself turns out to be wrong
// in some case not anticipated here, one tick can only ever do this much
// damage, and the next tick gets a fresh chance to reconsider rather than
// compounding a bad call.
const hostnameReapMaxRowsPerTick = 20

// ReapOrphanedAppHostnames finds domain_hostnames rows bound to an
// (environment_id, app_name) pair that no longer has a live App
// resource_snapshot behind it, and drives those rows into the same terminal
// shape demoteAppHostnames stamps at delete time: status='failed',
// cert_status='failed', status_reason=hostnameReasonAppDeleted,
// reattach_count=0, operation_id=NULL. demoteAppHostnames only runs inside
// DeleteApp's own transaction, so every hostname row orphaned by an app
// deleted before that safeguard existed -- and any row this pass itself has
// not yet reached -- is left pointing at nothing, feeding
// overviewDomainIssues as a fake, permanent poison-pill (see project memory
// project_deleteapp_orphans_domain_row_under_live_app.md). This pass is the
// forward-running, idempotent cleanup for that tail: its own WHERE excludes
// rows already sitting in that exact terminal shape, so a row it touched
// last tick is not touched again and updated_at does not keep advancing.
//
// "Does this app still exist" is answered the same way
// BackfillMissingDefaultDomains and ReattachOrphanedHostnames already answer
// it: a resource_snapshots row of kind='App' matching (environment_id, name),
// filtered through notOrphanedSnapshot. That is the one source of truth this
// file already trusts for that exact question; inventing a second one here
// would just be a second thing that could disagree with it.
//
// The two existing passes both use a missing resource_snapshot row to decide
// NOT to act (skip a backfill, skip a reattach), so if resource_snapshots
// were empty or stale for an environment -- the sync job for that namespace
// wedged or lost its data -- they simply do nothing, which is safe. This pass
// runs that check backwards: it wants to act, demoting a row, BECAUSE a
// resource_snapshot is missing. The same blindness that leaves the other two
// passes harmlessly inert would make this one demote every hostname in the
// environment, including ones sitting under apps that are alive and well.
// Guarding against that needs a positive signal that the blindness has NOT
// happened, not just the absence of the one row being checked -- so before
// touching any row in an environment, this pass requires that same
// environment to have at least one OTHER App resource_snapshot synced within
// hostnameReapEnvFreshnessWindow. That is proof the snapshot pipeline for
// this specific environment is alive right now, not just that it once was.
//
// That freshness guard has one structural hole, and it is the common case
// rather than an edge case: an environment whose only app was the one just
// deleted can never produce another fresh App snapshot, so no amount of
// waiting will ever satisfy it. Ten such rows sat permanently in
// overviewDomainIssues, incurable by definition -- the panel called them
// breakage forever because the pass could not tell "this app is gone" apart
// from "this environment went blind".
//
// So liveness is proven either way: the fresh sibling snapshot above, OR a
// committed DeleteApp row in operations for this exact
// (environment_id, app_name). The second proof is immune to the blindness
// the first one guards against, because operations is written by the API
// handler inside DeleteApp's own transaction and never derived from the
// snapshot pipeline -- a wedged or empty resource_snapshots cannot fabricate
// it. It is positive evidence that this app was deliberately deleted, which
// is strictly stronger than the absence of a snapshot row.
//
// audit_events carries the same intent but cannot be used here: its
// environment_id is NULL on most historical DeleteApp rows, so there is no
// reliable key to join a hostname to.
//
// Name reuse is the one way that proof could go stale: delete "api", create
// "api" again in the same environment, and the old DeleteApp row would
// authorize demoting the new app's live hostname. Hence the guard against
// any CreateApp operation for the same (environment_id, resource_name) newer
// than the DeleteApp -- a recreated app leaves that row behind and this pass
// falls back to needing the snapshot proof.
func ReapOrphanedAppHostnames(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx,
		`UPDATE domain_hostnames dh
		    SET status = 'failed', cert_status = 'failed', status_reason = $1,
		        reattach_count = 0, operation_id = NULL, updated_at = now()
		  WHERE dh.id IN (
		      SELECT dh2.id
		        FROM domain_hostnames dh2
		        JOIN environments e ON e.id = dh2.environment_id
		       WHERE e.runtime = $2
		         AND (dh2.status <> 'failed' OR dh2.status_reason IS DISTINCT FROM $1)
		         AND NOT EXISTS (
		             SELECT 1 FROM resource_snapshots rs
		              WHERE rs.environment_id = dh2.environment_id
		                AND rs.kind = 'App' AND rs.name = dh2.app_name
		                AND `+notOrphanedSnapshot+`
		         )
		         AND (
		             EXISTS (
		                 SELECT 1 FROM resource_snapshots rs
		                  WHERE rs.environment_id = dh2.environment_id
		                    AND rs.kind = 'App' AND `+notOrphanedSnapshot+`
		                    AND rs.last_synced_at > now() - ($3 * interval '1 second')
		             )
		             OR EXISTS (
		                 SELECT 1 FROM operations o
		                  WHERE o.environment_id = dh2.environment_id
		                    AND o.resource_kind = 'App' AND o.resource_name = dh2.app_name
		                    AND o.action = 'DeleteApp' AND o.status = $5
		                    AND NOT EXISTS (
		                        SELECT 1 FROM operations o2
		                         WHERE o2.environment_id = o.environment_id
		                           AND o2.resource_kind = 'App'
		                           AND o2.resource_name = o.resource_name
		                           AND o2.action = 'CreateApp'
		                           AND o2.created_at > o.created_at
		                    )
		             )
		         )
		       ORDER BY dh2.updated_at ASC
		       LIMIT $4
		  )`,
		hostnameReasonAppDeleted, models.EnvironmentRuntimeK8s,
		hostnameReapEnvFreshnessWindow.Seconds(), hostnameReapMaxRowsPerTick,
		models.OperationStatusCommitted,
	)
	return err
}

// reattachOrphanedHostname re-drives one failed domain_hostnames row: a fresh
// operation (AttachDefaultDomain for managed/surrogate rows,
// AttachCustomHostname for unmanaged/custom rows) re-renders the hostname's
// Ingress into git, and the row itself goes back to pending with a reset
// attach clock, in one transaction so the row and its provisioning operation
// never diverge. The UPDATE is guarded on status='failed' so a row that
// changed underneath the SELECT (concurrent manual re-attach, another
// maintenance tick) is left alone rather than clobbered; when that guard
// finds nothing to update the whole transaction is rolled back instead of
// committing an operation with no matching row change.
func reattachOrphanedHostname(ctx context.Context, pool *pgxpool.Pool, hostnameID, projectID, environmentID uuid.UUID, appName, hostname, action string) error {
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
		 VALUES ($1, $2, $3, $4, 'App', $5, 'Created', $6)
		 RETURNING id`,
		reissueActorID, projectID, environmentID, action, appName, payload,
	).Scan(&opID); err != nil {
		return err
	}
	ct, err := tx.Exec(ctx,
		`UPDATE domain_hostnames
		    SET status = 'pending', cert_status = 'pending', status_reason = NULL,
		        operation_id = $2, attach_started_at = now(),
		        reattach_count = reattach_count + 1, updated_at = now()
		  WHERE id = $1 AND status = 'failed'`,
		hostnameID, opID,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return nil
	}
	return tx.Commit(ctx)
}

// hostnameProbeTargets resolves where to aim the two checks for one pending
// hostname: probeAddr is the host:port to hand hostnameCertLive, dnsTarget is
// the IP the user's own record must resolve to for the reason code. It reports
// probeable=false when neither can be determined, in which case the row is left
// untouched rather than probed at an address that cannot answer for it.
//
// Kubernetes environments terminate TLS at our shared ingress, so the probe
// goes to cfg.IngressTLSProbeAddr and the record must point at
// cfg.CustomDomainATarget. VM environments terminate on the VM itself, so both
// follow the enrolled app server's IP -- probing the cluster ingress for a VM
// hostname would report "not live" forever. A VM environment with no IP
// recorded yet has nothing to probe at all.
//
// With no config (tests) the probe falls back to the public hostname and no DNS
// target is claimed, so the reason stays cert_pending rather than asserting a
// DNS fault it has no target to check against.
func hostnameProbeTargets(cfg *config.Config, hostname string, runtime models.EnvironmentRuntime, vmIP *string) (probeAddr, dnsTarget string, probeable bool) {
	if runtime == models.EnvironmentRuntimeVM {
		if vmIP == nil || *vmIP == "" {
			return "", "", false
		}
		return net.JoinHostPort(*vmIP, "443"), *vmIP, true
	}
	if cfg == nil {
		return net.JoinHostPort(hostname, "443"), "", true
	}
	probeAddr = cfg.IngressTLSProbeAddr
	if probeAddr == "" {
		probeAddr = net.JoinHostPort(hostname, "443")
	}
	dnsTarget = cfg.CustomDomainATarget
	if dnsTarget == "" {
		dnsTarget = cfg.ClusterLBIP
	}
	return probeAddr, dnsTarget, true
}

// hostnameCertLive reports whether addr completes a TLS handshake, with SNI set
// to hostname, using a publicly-trusted certificate valid for that hostname.
// The default (verifying) tls.Config means a self-signed / ingress-default /
// expired cert fails the handshake, so only a genuinely issued LE cert returns
// true.
//
// addr is deliberately a separate argument from hostname: it points at the
// server WE expect to serve the name (our ingress, or the VM host), so a
// successful handshake proves we serve it. Dialling the hostname itself proves
// only that somebody does, which for a domain still delegated to its previous
// provider is always true and always wrong.
func hostnameCertLive(ctx context.Context, hostname, addr string) bool {
	if addr == "" {
		addr = net.JoinHostPort(hostname, "443")
	}
	dctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	dialer := tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 5 * time.Second},
		Config:    &tls.Config{ServerName: hostname},
	}
	conn, err := dialer.DialContext(dctx, "tcp", addr)
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

// hashFragment returns the first 4 hex characters of sha256(s), the same
// deterministic-suffix idiom as gitops-agent's ScopedArgoName and build-agent's
// buildDefaultHostname/buildPreviewHostname (internal/db/deploy.go) — kept
// byte-for-byte identical here since backend and build-agent are separate Go
// modules and cannot share the helper directly.
func hashFragment(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:4]
}

// capFragment shrinks a variable hostname fragment (here, the app-name label)
// so that fixedLen+len(result) never exceeds 63 — the DNS-1123 label limit
// gitops-agent's FQDNToName applies to the FULL fqdn (dots become dashes, so
// the whole hostname becomes a k8s resource name, not just its first
// dot-separated label). fixedLen is the byte length of every other part of
// the final hostname. When the untouched fragment already fits, it is
// returned unchanged so short/existing hostnames stay byte-for-byte identical
// to before this cap existed. Otherwise the fragment is truncated and a short
// deterministic hash of the FULL untouched fragment is appended, so distinct
// long fragments never collide once truncated to the same prefix.
func capFragment(fragment string, fixedLen int) string {
	if fixedLen+len(fragment) <= 63 {
		return fragment
	}
	hash := hashFragment(fragment)
	maxFrag := 63 - fixedLen - 1 - len(hash)
	if maxFrag < 0 {
		maxFrag = 0
	}
	trimmed := fragment
	if len(trimmed) > maxFrag {
		trimmed = trimmed[:maxFrag]
	}
	trimmed = strings.TrimRight(trimmed, "-")
	if trimmed == "" {
		return hash
	}
	return trimmed + "-" + hash
}

func buildDefaultHostname(base, name, suffix string) string {
	fixedLen := 1 + len(suffix) + 1 + len(base)
	label := capFragment(name, fixedLen)
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

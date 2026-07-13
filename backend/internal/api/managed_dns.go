package api

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/pdns"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// allowedRecordTypes are the record types the managed-DNS editor accepts.
// Managed DNS (NS delegation): a verified apex authorization can be switched to
// "delegated" mode, where the console creates a PowerDNS zone, seeds apex/www
// routing, and exposes a record editor. PowerDNS is the source of truth for
// records (not mirrored in our DB). Every endpoint is gated on POWERDNS_API_KEY
// (503 otherwise) and requires project write access.
var allowedRecordTypes = map[string]bool{
	"A": true, "AAAA": true, "CNAME": true, "MX": true,
	"TXT": true, "NS": true, "SRV": true, "CAA": true,
}

// managedZone is the managed_zones row surfaced to the console.
type managedZone struct {
	ID              uuid.UUID `json:"id"`
	AuthorizationID uuid.UUID `json:"authorization_id"`
	Apex            string    `json:"apex"`
	PDNSZone        string    `json:"pdns_zone"`
	Status          string    `json:"status"`
}

// managedDNSAuth resolves the authorization the request targets, enforcing the
// managed-DNS feature gate and project write access. On any failure it writes the
// response and returns ok=false. It returns the apex the authorization owns.
func (h *Handler) managedDNSAuth(c *gin.Context) (projectID, authID uuid.UUID, apex string, ok bool) {
	if h.cfg.PowerDNSAPIKey == "" || h.pdns == nil {
		respondError(c, http.StatusServiceUnavailable, "managed DNS not configured")
		return uuid.Nil, uuid.Nil, "", false
	}
	claims, has := auth.GetClaims(c)
	if !has {
		respondUnauthorized(c)
		return uuid.Nil, uuid.Nil, "", false
	}
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return uuid.Nil, uuid.Nil, "", false
	}
	authID, err = uuid.Parse(c.Param("authId"))
	if err != nil {
		respondNotFound(c)
		return uuid.Nil, uuid.Nil, "", false
	}
	role, err := h.effectiveRole(c.Request.Context(), claims, projectID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return uuid.Nil, uuid.Nil, "", false
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return uuid.Nil, uuid.Nil, "", false
	}
	if !canWrite(role) {
		respondForbidden(c)
		return uuid.Nil, uuid.Nil, "", false
	}
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT apex_domain FROM domain_authorizations WHERE id = $1 AND project_id = $2`,
		authID, projectID,
	).Scan(&apex)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return uuid.Nil, uuid.Nil, "", false
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load authorization")
		return uuid.Nil, uuid.Nil, "", false
	}
	return projectID, authID, apex, true
}

// DelegateAuthorization switches an apex authorization to delegated mode. It
// creates a PowerDNS zone delegated to the platform nameservers, seeds apex/www
// A records pointing at the ingress LB, records a managed_zones row, and returns
// the nameservers the user must set at their registrar.
//
// @ID          delegateAuthorization
// @Summary     Delegate an apex domain to the platform nameservers
// @Description Creates a managed PowerDNS zone for a verified apex authorization, seeds apex + www routing, and returns the nameservers (ns1/ns2) the user sets at their registrar. Once delegation propagates, the zone flips to active.
// @Tags        domain
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       authId    path     string true "Authorization UUID"
// @Success     201       {object} map[string]interface{}
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/domains/authorizations/{authId}/delegate [post]
func (h *Handler) DelegateAuthorization(c *gin.Context) {
	_, authID, apex, ok := h.managedDNSAuth(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	if err := h.pdns.CreateZone(ctx, apex, h.cfg.PlatformNameservers); err != nil && !pdns.IsConflict(err) {
		respondError(c, http.StatusBadGateway, "failed to create zone: "+err.Error())
		return
	}
	target := h.cfg.CustomDomainATarget
	if err := h.pdns.UpsertRecord(ctx, apex, apex, "A", 300, []string{target}); err != nil {
		respondError(c, http.StatusBadGateway, "failed to seed apex record: "+err.Error())
		return
	}
	if err := h.pdns.UpsertRecord(ctx, apex, "www."+apex, "A", 300, []string{target}); err != nil {
		respondError(c, http.StatusBadGateway, "failed to seed www record: "+err.Error())
		return
	}

	pdnsZone := pdns.QualifyName(apex, apex)
	var mz managedZone
	err := h.pool.QueryRow(ctx,
		`INSERT INTO managed_zones (authorization_id, apex, pdns_zone, status)
		 VALUES ($1, $2, $3, 'awaiting_ns')
		 ON CONFLICT (apex) DO UPDATE SET pdns_zone = EXCLUDED.pdns_zone, updated_at = NOW()
		 RETURNING id, authorization_id, apex, pdns_zone, status`,
		authID, apex, pdnsZone,
	).Scan(&mz.ID, &mz.AuthorizationID, &mz.Apex, &mz.PDNSZone, &mz.Status)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to record managed zone")
		return
	}

	if _, err := h.pool.Exec(ctx,
		`UPDATE domain_authorizations SET delegation_mode = 'delegated', updated_at = NOW() WHERE id = $1`,
		authID,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to update delegation mode")
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"nameservers": h.cfg.PlatformNameservers,
		"zone":        mz.Apex,
		"status":      mz.Status,
	})
}

// GetManagedZone returns the managed_zones row plus the live rrsets from PowerDNS.
//
// @ID          getManagedZone
// @Summary     Get a delegated apex's managed zone + records
// @Tags        domain
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       authId    path     string true "Authorization UUID"
// @Success     200       {object} map[string]interface{}
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/domains/authorizations/{authId}/zone [get]
func (h *Handler) GetManagedZone(c *gin.Context) {
	_, authID, apex, ok := h.managedDNSAuth(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	mz, found, err := h.loadManagedZone(ctx, authID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load managed zone")
		return
	}
	if !found {
		respondNotFound(c)
		return
	}
	rrsets, err := h.pdns.ListRecords(ctx, apex)
	if err != nil {
		respondError(c, http.StatusBadGateway, "failed to read zone: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"zone":        mz.Apex,
		"status":      mz.Status,
		"nameservers": h.cfg.PlatformNameservers,
		"rrsets":      rrsetsToView(rrsets),
	})
}

// ListManagedRecords lists the live records of a delegated apex from PowerDNS.
//
// @ID          listManagedRecords
// @Summary     List records of a delegated apex
// @Tags        domain
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       authId    path     string true "Authorization UUID"
// @Success     200       {object} map[string]interface{}
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/domains/authorizations/{authId}/zone/records [get]
func (h *Handler) ListManagedRecords(c *gin.Context) {
	_, authID, apex, ok := h.managedDNSAuth(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	if _, found, err := h.loadManagedZone(ctx, authID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load managed zone")
		return
	} else if !found {
		respondNotFound(c)
		return
	}
	rrsets, err := h.pdns.ListRecords(ctx, apex)
	if err != nil {
		respondError(c, http.StatusBadGateway, "failed to read zone: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"records": rrsetsToView(rrsets)})
}

type upsertRecordRequest struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	TTL      int      `json:"ttl"`
	Contents []string `json:"contents"`
}

// managedRecordView is the flat record shape the console consumes, matching the
// upsert request body and the import-preview output. PowerDNS returns rrsets as
// {name,type,ttl,records:[{content}]}; the console reads {name,type,ttl,contents},
// so live rrsets are flattened before they leave the API.
type managedRecordView struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	TTL      int      `json:"ttl"`
	Contents []string `json:"contents"`
}

func rrsetsToView(rrsets []pdns.RRSet) []managedRecordView {
	out := make([]managedRecordView, 0, len(rrsets))
	for _, rr := range rrsets {
		contents := make([]string, 0, len(rr.Records))
		for _, rec := range rr.Records {
			contents = append(contents, rec.Content)
		}
		out = append(out, managedRecordView{Name: rr.Name, Type: rr.Type, TTL: rr.TTL, Contents: contents})
	}
	return out
}

// UpsertManagedRecord replaces one rrset in a delegated apex's zone.
//
// @ID          upsertManagedRecord
// @Summary     Create or replace a record in a delegated apex
// @Description Upserts one rrset (name/type/ttl/contents) via PowerDNS. The apex NS rrset and the SOA are protected and cannot be modified.
// @Tags        domain
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string              true "Project UUID"
// @Param       authId    path     string              true "Authorization UUID"
// @Param       body      body     upsertRecordRequest true "Record"
// @Success     200       {object} map[string]interface{}
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/domains/authorizations/{authId}/zone/records [post]
func (h *Handler) UpsertManagedRecord(c *gin.Context) {
	_, authID, apex, ok := h.managedDNSAuth(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	var req upsertRecordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	rrType := strings.ToUpper(strings.TrimSpace(req.Type))
	if !allowedRecordTypes[rrType] {
		respondError(c, http.StatusBadRequest, "unsupported record type")
		return
	}
	if len(req.Contents) == 0 {
		respondError(c, http.StatusBadRequest, "contents must not be empty")
		return
	}
	if msg, protected := protectedRecord(apex, req.Name, rrType); protected {
		respondError(c, http.StatusBadRequest, msg)
		return
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = 300
	}

	if _, found, err := h.loadManagedZone(ctx, authID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load managed zone")
		return
	} else if !found {
		respondNotFound(c)
		return
	}

	if err := h.pdns.UpsertRecord(ctx, apex, req.Name, rrType, ttl, req.Contents); err != nil {
		respondError(c, http.StatusBadGateway, "failed to upsert record: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"record": gin.H{
			"name":     pdns.QualifyName(apex, req.Name),
			"type":     rrType,
			"ttl":      ttl,
			"contents": req.Contents,
		},
	})
}

type deleteRecordRequest struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// DeleteManagedRecord removes one rrset from a delegated apex's zone.
//
// @ID          deleteManagedRecord
// @Summary     Delete a record from a delegated apex
// @Description Deletes one rrset (name/type) via PowerDNS. The apex NS rrset and the SOA are protected and cannot be removed. name/type may be supplied in the JSON body or as query parameters.
// @Tags        domain
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       authId    path     string true "Authorization UUID"
// @Param       name      query    string false "Record name"
// @Param       type      query    string false "Record type"
// @Success     204
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/domains/authorizations/{authId}/zone/records [delete]
func (h *Handler) DeleteManagedRecord(c *gin.Context) {
	_, authID, apex, ok := h.managedDNSAuth(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	var req deleteRecordRequest
	_ = c.ShouldBindJSON(&req)
	name := req.Name
	if name == "" {
		name = c.Query("name")
	}
	rrType := strings.ToUpper(strings.TrimSpace(req.Type))
	if rrType == "" {
		rrType = strings.ToUpper(strings.TrimSpace(c.Query("type")))
	}
	if rrType == "" || !allowedRecordTypes[rrType] {
		respondError(c, http.StatusBadRequest, "unsupported or missing record type")
		return
	}
	if msg, protected := protectedRecord(apex, name, rrType); protected {
		respondError(c, http.StatusBadRequest, msg)
		return
	}

	if _, found, err := h.loadManagedZone(ctx, authID); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load managed zone")
		return
	} else if !found {
		respondNotFound(c)
		return
	}

	if err := h.pdns.DeleteRecord(ctx, apex, name, rrType); err != nil {
		respondError(c, http.StatusBadGateway, "failed to delete record: "+err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

// loadManagedZone loads the managed_zones row for an authorization. found is
// false (no error) when the authorization has no delegated zone.
func (h *Handler) loadManagedZone(ctx context.Context, authID uuid.UUID) (managedZone, bool, error) {
	var mz managedZone
	err := h.pool.QueryRow(ctx,
		`SELECT id, authorization_id, apex, pdns_zone, status
		 FROM managed_zones WHERE authorization_id = $1`,
		authID,
	).Scan(&mz.ID, &mz.AuthorizationID, &mz.Apex, &mz.PDNSZone, &mz.Status)
	if err == pgx.ErrNoRows {
		return managedZone{}, false, nil
	}
	if err != nil {
		return managedZone{}, false, err
	}
	return mz, true, nil
}

// protectedRecord reports whether editing (name, rrType) would break the zone's
// delegation NS set or its SOA, returning a human-readable reason when so.
func protectedRecord(apex, name, rrType string) (string, bool) {
	if rrType == "SOA" {
		return "the SOA record cannot be modified", true
	}
	if rrType == "NS" && pdns.QualifyName(apex, name) == pdns.QualifyName(apex, apex) {
		return "the apex NS records cannot be modified", true
	}
	return "", false
}

// PollPendingDelegations advances managed zones from awaiting_ns to active once
// the apex's authoritative NS set includes the platform nameservers. It resolves
// each pending apex's NS records and flips status when a platform NS is present.
// last_checked_at is bumped every run. Called on a ticker from main.
func PollPendingDelegations(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config) error {
	rows, err := pool.Query(ctx,
		`SELECT id, apex FROM managed_zones WHERE status = 'awaiting_ns'`)
	if err != nil {
		return err
	}
	type pendingZone struct {
		id   uuid.UUID
		apex string
	}
	var pending []pendingZone
	for rows.Next() {
		var p pendingZone
		if err := rows.Scan(&p.id, &p.apex); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, p)
	}
	rows.Close()

	want := map[string]bool{}
	for _, ns := range cfg.PlatformNameservers {
		want[strings.TrimSuffix(strings.ToLower(strings.TrimSpace(ns)), ".")] = true
	}

	for _, p := range pending {
		if delegationDetected(ctx, p.apex, want) {
			_, _ = pool.Exec(ctx,
				`UPDATE managed_zones SET status = 'active', last_checked_at = NOW(), updated_at = NOW()
				 WHERE id = $1 AND status = 'awaiting_ns'`, p.id)
		} else {
			_, _ = pool.Exec(ctx,
				`UPDATE managed_zones SET last_checked_at = NOW() WHERE id = $1`, p.id)
		}
	}
	return nil
}

// delegationDetected resolves the apex's NS set and reports whether any of the
// platform nameservers (want) appears.
func delegationDetected(ctx context.Context, apex string, want map[string]bool) bool {
	lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var resolver net.Resolver
	nss, err := resolver.LookupNS(lookupCtx, apex)
	if err != nil {
		return false
	}
	for _, ns := range nss {
		if want[strings.TrimSuffix(strings.ToLower(ns.Host), ".")] {
			return true
		}
	}
	return false
}

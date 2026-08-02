package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// importRecord is one record surfaced by the import preview and accepted back by
// the import endpoint. contents are already in PowerDNS content form (e.g. an MX
// as "10 mail.example.com.").
type importRecord struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	TTL      int      `json:"ttl"`
	Contents []string `json:"contents"`
}

type importZoneRequest struct {
	Records []importRecord `json:"records"`
}

// importLookupTimeout bounds each individual DNS lookup during a preview so one
// slow/hung name can't stall the whole request.
const importLookupTimeout = 3 * time.Second

// PreviewZoneImport probes the apex's CURRENT live DNS (apex A/AAAA/MX/TXT and
// www A/AAAA/CNAME) via the default public resolver and returns the found records
// as import suggestions. Because the domain is still delegated to the user's old
// nameservers, the public resolver returns their live site/email records. This is
// read-only: it writes nothing to PowerDNS. Each lookup is best-effort -- a failed
// or empty lookup simply omits that record rather than failing the request. NS and
// SOA are never included (those are the platform's).
//
// @ID          previewZoneImport
// @Summary     Preview importable live records for a delegated apex
// @Description Probes the apex's current authoritative DNS (via the public resolver) for apex A/AAAA/MX/TXT and www A/AAAA/CNAME and returns them as import suggestions with a default TTL of 300. Read-only; writes nothing. Lookups are best-effort and NS/SOA are excluded.
// @Tags        domain
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       authId    path     string true "Authorization UUID"
// @Success     200       {object} map[string]interface{}
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/domains/authorizations/{authId}/zone/import-preview [get]
func (h *Handler) PreviewZoneImport(c *gin.Context) {
	_, _, apex, ok := h.managedDNSAuth(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	var resolver net.Resolver
	records := make([]importRecord, 0, 8)

	if ips := lookupIPs(ctx, &resolver, "ip4", apex); len(ips) > 0 {
		records = append(records, importRecord{Name: apex, Type: "A", TTL: 300, Contents: ips})
	}
	if ips := lookupIPs(ctx, &resolver, "ip6", apex); len(ips) > 0 {
		records = append(records, importRecord{Name: apex, Type: "AAAA", TTL: 300, Contents: ips})
	}
	if mx := lookupMX(ctx, &resolver, apex); len(mx) > 0 {
		records = append(records, importRecord{Name: apex, Type: "MX", TTL: 300, Contents: mx})
	}
	if txt := lookupTXT(ctx, &resolver, apex); len(txt) > 0 {
		records = append(records, importRecord{Name: apex, Type: "TXT", TTL: 300, Contents: txt})
	}
	records = append(records, probeWWW(ctx, &resolver, apex)...)

	c.JSON(http.StatusOK, gin.H{"records": records})
}

// ImportZone writes user-confirmed records into the delegated apex's PowerDNS
// zone. Each record is validated against the managed-DNS record allow-list (NS
// and SOA are rejected) and upserted; any that fail validation or the PowerDNS
// write are skipped and reported. Requires the apex to already have a managed
// zone (the user must have delegated first).
//
// @ID          importZone
// @Summary     Import confirmed records into a delegated apex
// @Description Upserts each supplied record (name/type/ttl/contents) into the apex's PowerDNS zone. NS/SOA and the protected apex NS are rejected; TXT contents are quoted if needed. Records that fail validation or the write are skipped and returned. Requires an existing managed zone.
// @Tags        domain
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string            true "Project UUID"
// @Param       authId    path     string            true "Authorization UUID"
// @Param       body      body     importZoneRequest true "Records to import"
// @Success     200       {object} map[string]interface{}
// @Failure     400       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/domains/authorizations/{authId}/zone/import [post]
func (h *Handler) ImportZone(c *gin.Context) {
	projectID, authID, apex, ok := h.managedDNSAuthFor(c, "ImportZone")
	if !ok {
		return
	}
	ctx := c.Request.Context()

	audit := h.dnsAudit(c, projectID, "ImportZone", apex)
	reject := func(status int, reason string) {
		audit(auditOutcomeFailure, map[string]any{"reason": reason, "status": status})
	}

	var req importZoneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		reject(http.StatusBadRequest, "malformed_body")
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	if _, found, err := h.loadManagedZone(ctx, authID); err != nil {
		reject(http.StatusInternalServerError, "zone_lookup_failed")
		respondError(c, http.StatusInternalServerError, "failed to load managed zone")
		return
	} else if !found {
		reject(http.StatusNotFound, "zone_not_delegated")
		respondNotFound(c)
		return
	}

	imported := 0
	skipped := make([]gin.H, 0)
	for _, rec := range req.Records {
		rrType := strings.ToUpper(strings.TrimSpace(rec.Type))
		if !allowedRecordTypes[rrType] || rrType == "NS" {
			skipped = append(skipped, gin.H{"name": rec.Name, "type": rec.Type, "reason": "unsupported record type"})
			continue
		}
		if len(rec.Contents) == 0 {
			skipped = append(skipped, gin.H{"name": rec.Name, "type": rrType, "reason": "empty contents"})
			continue
		}
		if msg, protected := protectedRecord(apex, rec.Name, rrType); protected {
			skipped = append(skipped, gin.H{"name": rec.Name, "type": rrType, "reason": msg})
			continue
		}
		contents := rec.Contents
		if rrType == "TXT" {
			contents = quoteTXTContents(contents)
		}
		ttl := rec.TTL
		if ttl <= 0 {
			ttl = 300
		}
		if err := h.pdns.UpsertRecord(ctx, apex, rec.Name, rrType, ttl, contents); err != nil {
			skipped = append(skipped, gin.H{"name": rec.Name, "type": rrType, "reason": err.Error()})
			continue
		}
		imported++
	}

	skippedNames := make([]string, 0, len(skipped))
	for _, s := range skipped {
		skippedNames = append(skippedNames, fmt.Sprintf("%v/%v", s["name"], s["type"]))
	}
	audit(auditOutcomeSuccess, map[string]any{
		"imported": imported, "skipped": len(skipped), "skipped_records": skippedNames,
	})

	c.JSON(http.StatusOK, gin.H{"imported": imported, "skipped": skipped})
}

// lookupIPs resolves host for the given IP network ("ip4" or "ip6") and returns
// the addresses as strings, or nil on any error/timeout.
func lookupIPs(parent context.Context, r *net.Resolver, network, host string) []string {
	ctx, cancel := context.WithTimeout(parent, importLookupTimeout)
	defer cancel()
	ips, err := r.LookupIP(ctx, network, host)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return out
}

// lookupMX resolves host's MX records into PowerDNS content form ("<pref> <host>"),
// or nil on any error/timeout.
func lookupMX(parent context.Context, r *net.Resolver, host string) []string {
	ctx, cancel := context.WithTimeout(parent, importLookupTimeout)
	defer cancel()
	mxs, err := r.LookupMX(ctx, host)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(mxs))
	for _, mx := range mxs {
		out = append(out, fmt.Sprintf("%d %s", mx.Pref, mx.Host))
	}
	return out
}

// lookupTXT resolves host's TXT records, or nil on any error/timeout.
func lookupTXT(parent context.Context, r *net.Resolver, host string) []string {
	ctx, cancel := context.WithTimeout(parent, importLookupTimeout)
	defer cancel()
	txts, err := r.LookupTXT(ctx, host)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(txts))
	for _, t := range txts {
		if strings.TrimSpace(t) != "" {
			out = append(out, t)
		}
	}
	return out
}

// probeWWW resolves the www subdomain, preferring a CNAME when present and
// otherwise emitting A/AAAA records. Returns nil when nothing resolves.
func probeWWW(parent context.Context, r *net.Resolver, apex string) []importRecord {
	www := "www." + apex
	if cname := lookupCNAME(parent, r, www); cname != "" {
		target := strings.TrimSuffix(strings.ToLower(cname), ".")
		if target != strings.TrimSuffix(strings.ToLower(www), ".") &&
			target != strings.TrimSuffix(strings.ToLower(apex), ".") {
			return []importRecord{{Name: www, Type: "CNAME", TTL: 300, Contents: []string{cname}}}
		}
	}
	records := make([]importRecord, 0, 2)
	if ips := lookupIPs(parent, r, "ip4", www); len(ips) > 0 {
		records = append(records, importRecord{Name: www, Type: "A", TTL: 300, Contents: ips})
	}
	if ips := lookupIPs(parent, r, "ip6", www); len(ips) > 0 {
		records = append(records, importRecord{Name: www, Type: "AAAA", TTL: 300, Contents: ips})
	}
	return records
}

// lookupCNAME returns host's canonical name, or "" on error/timeout.
func lookupCNAME(parent context.Context, r *net.Resolver, host string) string {
	ctx, cancel := context.WithTimeout(parent, importLookupTimeout)
	defer cancel()
	cname, err := r.LookupCNAME(ctx, host)
	if err != nil {
		return ""
	}
	return cname
}

// quoteTXTContents wraps each TXT value in double quotes (escaping embedded
// quotes) unless it is already quoted, matching the form PowerDNS expects.
func quoteTXTContents(contents []string) []string {
	out := make([]string, 0, len(contents))
	for _, s := range contents {
		if len(s) >= 2 && strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") {
			out = append(out, s)
			continue
		}
		out = append(out, "\""+strings.ReplaceAll(s, "\"", "\\\"")+"\"")
	}
	return out
}

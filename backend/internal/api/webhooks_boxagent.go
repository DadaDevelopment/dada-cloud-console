package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/models"
)

// box-agent ingest webhooks: status transitions and out-of-guest samples.
//
// DELIBERATELY UN-ANNOTATED, and registered inside the `if verifier ok` guard in
// router.go, exactly like webhooks_dadagent.go. That is not laziness, it is what
// keeps two gates green and one boundary intact:
//
//   - openapi_coverage_test.go enumerates the routes SetupRouter actually
//     registers under the test config. Because the guard fails to build a
//     verifier under that config, these routes are not registered there, so the
//     coverage gate passes without the routes appearing in swagger.json.
//   - the MCP tool surface is REFLECTED from swagger.json. The standalone
//     mcp-server curates with a DENYLIST rather than an allowlist, so anything
//     that reaches the spec becomes a tool there by default. Annotating these two
//     would publish platform-internal ingest tools — "set a box's status", "write
//     a box's usage sample" — into an agent-facing toolset. An agent that can
//     write its own billing sample is not a bug we want to discover later.
//
// TENANCY IS NEVER TRUSTED FROM THE AGENT. Every request resolves project_id and
// the owning environment from instance_ref against the boxes table. A box host
// runs hostile code by design; if it could name its own tenant it could write
// into another customer's box.

// boxAgentStatusCallback is the status transition a box-agent reports.
//
// Timestamps are absent on purpose. The orchestrator's clock is the only clock
// allowed to measure this path (internal/box.PhaseTimeline has no method that
// accepts a caller-supplied instant), and a freshly booted box's clock is exactly
// the thing that is wrong. So the agent reports WHAT happened; WHEN is ours.
type boxAgentStatusCallback struct {
	InstanceRef string `json:"instance_ref"`
	Status      string `json:"status"`
	NodeRef     string `json:"node_ref"`
	SSHHost     string `json:"ssh_host"`
	SSHPort     int    `json:"ssh_port"`
	MCPURL      string `json:"mcp_url"`
	Error       string `json:"error"`
}

// boxAgentSampleCallback is one out-of-guest activity sample.
//
// This is the AUTHORITATIVE billing signal: it is taken from outside the guest,
// by our agent, watching the sandbox. A heartbeat from inside the guest exists
// only to DELAY sleep — it can ask for more billing, never less — and that
// asymmetry is what makes trusting the in-guest signal safe. A box runs as root
// under the customer's own agent, so an in-guest authoritative meter would let a
// customer under-report and would let anyone accuse us of over-reporting.
type boxAgentSampleCallback struct {
	InstanceRef string `json:"instance_ref"`
	// Active is the agent's verdict for the sampled window. Kept as a boolean the
	// agent computes rather than raw counters the backend thresholds, so the
	// activity rule lives next to the cgroup counters it reads.
	Active bool `json:"active"`
	// Sample is the opaque metadata blob (cpu, memory, egress bytes, open
	// sessions). Metadata only — never commands, keystrokes or tenant traffic
	// content. Stored verbatim in boxes.last_sample_json.
	//
	// ONE KEY IN IT IS READ BY THE BILLING PATH: cpu_percent, guest CPU as a
	// percentage of one core, measured by the agent from the cgroup counters
	// OUTSIDE the guest. box_meter.go thresholds it against BOX_ACTIVE_CPU_PERCENT
	// so a detached `cargo build` with nobody attached is still billed. The rest of
	// the blob stays uninterpreted.
	Sample json.RawMessage `json:"sample"`
	// GuestActive is the IN-GUEST agent's own claim, relayed by box-agent. It is a
	// POINTER so "the guest said idle" is distinguishable from "there was no
	// in-guest signal at all".
	//
	// It is admitted in exactly one direction, and that asymmetry is the integrity
	// property the whole meter rests on: true stamps guest_heartbeat_at, which can
	// only DEFER suspension and ADD billable minutes. false is DISCARDED here — it
	// is not written anywhere and no code path reads it — so a guest can never
	// reduce what it is billed, and by symmetry nobody can accuse us of having let
	// a guest inflate it downward and then over-reported. Trusting a signal that can
	// only cost its own sender money is safe.
	GuestActive *bool `json:"guest_active"`
}

// guestHeartbeatDefersOnly is the in-guest claim reduced to what the platform is
// willing to act on: a heartbeat instant, or nothing.
//
// Written as a separate function with its own name so the rule is a thing a reader
// can find and a reviewer can see being changed. A `false` from the guest returns
// false here and is then not written to any column, which is what makes "the guest
// cannot reduce billing" true structurally rather than by convention.
func guestHeartbeatDefersOnly(guestActive *bool) bool {
	return guestActive != nil && *guestActive
}

// resolvedBoxRef is the tenancy the backend derived itself from instance_ref.
type resolvedBoxRef struct {
	BoxID     uuid.UUID
	ProjectID uuid.UUID
	Status    models.BoxStatus
}

// resolveBoxByInstanceRef maps an opaque runtime handle to its box row. Returns
// pgx.ErrNoRows for an unknown or already-deleted instance; the caller answers
// 404 so a probing host learns nothing about which refs exist.
func (h *Handler) resolveBoxByInstanceRef(c *gin.Context, ref string) (resolvedBoxRef, error) {
	var out resolvedBoxRef
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT id, project_id, status FROM boxes
		  WHERE instance_ref = $1 AND status <> 'Deleted'`,
		ref,
	).Scan(&out.BoxID, &out.ProjectID, &out.Status)
	return out, err
}

// authorizeBoxAgent applies the same bearer-then-client gate as the dadagent
// webhook: a JWKS-verified token whose azp (or resource_access) is box-agent.
func (h *Handler) authorizeBoxAgent(c *gin.Context, verifier tokenVerifier) bool {
	header := c.GetHeader("Authorization")
	raw := strings.TrimPrefix(header, "Bearer ")
	if raw == "" || raw == header {
		respondUnauthorized(c)
		return false
	}
	if verifier == nil {
		respondError(c, http.StatusServiceUnavailable, "box-agent webhook not configured")
		return false
	}
	claims, err := verifier.Verify(c.Request.Context(), raw)
	if err != nil {
		respondUnauthorized(c)
		return false
	}
	if claims.Azp != "box-agent" && !hasClient(claims, "box-agent") {
		respondForbidden(c)
		return false
	}
	return true
}

// BoxAgentStatusWebhook ingests a box status transition from box-agent.
// Bearer-gated by JWKS (azp=box-agent); tenancy resolved from instance_ref.
// Intentionally carries no swaggo annotation — see the file comment.
func (h *Handler) BoxAgentStatusWebhook(c *gin.Context) {
	h.boxAgentStatusWebhook(c, h.boxAgentVerifier)
}

func (h *Handler) boxAgentStatusWebhook(c *gin.Context, verifier tokenVerifier) {
	if !h.authorizeBoxAgent(c, verifier) {
		return
	}

	var cb boxAgentStatusCallback
	if err := c.ShouldBindJSON(&cb); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if cb.InstanceRef == "" {
		respondError(c, http.StatusBadRequest, "instance_ref is required")
		return
	}
	next := models.BoxStatus(cb.Status)
	if !models.IsValidBoxStatus(next) {
		respondError(c, http.StatusBadRequest, "unknown status "+cb.Status)
		return
	}

	ref, err := h.resolveBoxByInstanceRef(c, cb.InstanceRef)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve box")
		return
	}

	// A retried or reordered callback must not resurrect a box. The state machine
	// lives in models.CanTransitionBoxStatus, so an illegal report is dropped with
	// 200 rather than 4xx: the agent did nothing wrong, its news is simply stale,
	// and a 4xx here would make it retry forever.
	if !models.CanTransitionBoxStatus(ref.Status, next) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "ignored": "stale transition",
			"from": ref.Status, "to": next})
		return
	}

	// last_active_at is stamped from the DATABASE clock (now()), never from the
	// payload: see the type comment on boxAgentStatusCallback.
	if _, err := h.pool.Exec(c.Request.Context(),
		`UPDATE boxes
		    SET status         = $2,
		        node_ref       = COALESCE(NULLIF($3, ''), node_ref),
		        ssh_host       = COALESCE(NULLIF($4, ''), ssh_host),
		        ssh_port       = COALESCE(NULLIF($5, 0), ssh_port),
		        mcp_url        = COALESCE(NULLIF($6, ''), mcp_url),
		        error_message  = CASE WHEN $2 = 'Failed' THEN $7 ELSE '' END,
		        last_active_at = CASE WHEN $2 IN ('Ready','Idle') THEN now() ELSE last_active_at END,
		        expires_at     = CASE WHEN $2 = 'Ready' AND expires_at IS NULL
		                              THEN now() + (ttl_seconds * INTERVAL '1 second')
		                              ELSE expires_at END,
		        slept_at       = CASE WHEN $2 = 'Sleeping' THEN now() ELSE NULL END,
		        deleted_at     = CASE WHEN $2 = 'Deleted' THEN now() ELSE deleted_at END,
		        updated_at     = now()
		  WHERE id = $1`,
		ref.BoxID, string(next), cb.NodeRef, cb.SSHHost, cb.SSHPort, cb.MCPURL, cb.Error,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to update box status")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// BoxAgentSampleWebhook ingests one out-of-guest activity sample from box-agent.
// Bearer-gated by JWKS (azp=box-agent); tenancy resolved from instance_ref.
// Intentionally carries no swaggo annotation — see the file comment.
func (h *Handler) BoxAgentSampleWebhook(c *gin.Context) {
	h.boxAgentSampleWebhook(c, h.boxAgentVerifier)
}

func (h *Handler) boxAgentSampleWebhook(c *gin.Context, verifier tokenVerifier) {
	if !h.authorizeBoxAgent(c, verifier) {
		return
	}

	var cb boxAgentSampleCallback
	if err := c.ShouldBindJSON(&cb); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if cb.InstanceRef == "" {
		respondError(c, http.StatusBadRequest, "instance_ref is required")
		return
	}

	ref, err := h.resolveBoxByInstanceRef(c, cb.InstanceRef)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve box")
		return
	}

	// An active sample also refreshes last_active_at, which is what the idle
	// reaper reads. An INACTIVE sample deliberately does not touch it: idleness is
	// the absence of activity, so it must not be recorded as an event.
	//
	// last_sample_active stores the out-of-guest verdict ITSELF, not just its
	// consequence. The meter needs the boolean because "the last sample said idle",
	// "no sample has arrived" and "something else bumped this box" are three
	// different states and last_active_at collapses them into one.
	//
	// guest_heartbeat_at is written only when the guest claimed activity
	// (guestHeartbeatDefersOnly). A guest claiming INACTIVITY changes nothing here —
	// no column moves — so the out-of-guest verdict above stands and the billed
	// minute is unaffected. That is the asymmetry, enforced by there being no
	// statement that could express the other direction.
	if _, err := h.pool.Exec(c.Request.Context(),
		`UPDATE boxes
		    SET last_sample_json   = COALESCE($2, last_sample_json),
		        last_sample_at     = now(),
		        last_sample_active = $3,
		        last_active_at     = CASE WHEN $3 OR $4 THEN now() ELSE last_active_at END,
		        guest_heartbeat_at = CASE WHEN $4 THEN now() ELSE guest_heartbeat_at END,
		        updated_at         = now()
		  WHERE id = $1`,
		ref.BoxID, cb.Sample, cb.Active, guestHeartbeatDefersOnly(cb.GuestActive),
	); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to record box sample")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

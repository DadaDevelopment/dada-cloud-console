package api

import (
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/metrics"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// expose: publish one port of a box.
//
// Two product rules are enforced by this handler rather than described elsewhere:
//
//   - The hostname is ASSIGNED BY THE PLATFORM under its wildcard and can never be
//     chosen by the caller. Custom domains are a crystallization feature, which is
//     also what removes most of the phishing incentive from a body that lives hours.
//   - Publishing is measured from the request to the first real 200, by cert path.
//     "The ingress object was created" is not the same event and is the one that
//     looks green while nothing answers.

type exposeBoxRequest struct {
	// Port is the port SERVING INSIDE the box. There is no hostname field on
	// purpose — see the file comment.
	Port int `json:"port"`
}

// ExposeBox publishes a port of the box through the platform edge.
//
// @ID          exposeBox
// @Summary     Publish a port of a box on a platform hostname
// @Description Publishes one port serving inside the box on a hostname the PLATFORM assigns under its wildcard. The caller cannot choose the hostname: custom domains are a crystallization feature, and a throwaway body with an arbitrary name is a phishing surface. Returns the assigned hostname and the URL that answers it, plus the measured time from the request to the first real 200 — not to "the route was created". Responses carry X-Robots-Tag: noindex, because an ephemeral body must not accumulate search presence it will outlive by hours.
// @Tags        box
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string           true "Project UUID"
// @Param       boxName   path     string           true "Box name"
// @Param       body      body     exposeBoxRequest true "The port serving inside the box"
// @Success     200       {object} map[string]interface{} "object with the assigned hostname and the URL that answers it"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string "the box is not in a phase that can publish a port"
// @Failure     503       {object} map[string]string "box runtime not configured"
// @Router      /projects/{projectId}/boxes/{boxName}/expose [post]
func (h *Handler) ExposeBox(c *gin.Context) {
	_, projectID, ok := h.boxWriteGate(c, true)
	if !ok {
		return
	}
	boxName := c.Param("boxName")
	audit := h.boxAudit(c, projectID, models.ActionExposeBox, boxName)
	stack, ok := h.requireBoxRuntime(c)
	if !ok {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "box_runtime_unavailable", "status": c.Writer.Status()})
		return
	}
	b, ok := h.resolveBox(c, projectID, boxName)
	if !ok {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "box_not_found", "status": c.Writer.Status()})
		return
	}
	audit = h.boxAuditFor(c, projectID, b, models.ActionExposeBox)
	var req exposeBoxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "malformed_body", "status": http.StatusBadRequest})
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.Port < 1 || req.Port > 65535 {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "port_out_of_range", "port": req.Port, "status": http.StatusBadRequest})
		respondError(c, http.StatusBadRequest, "port must be between 1 and 65535")
		return
	}
	if b.Status != models.BoxStatusReady && b.Status != models.BoxStatusIdle {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "phase_cannot_expose", "phase": string(b.Status), "port": req.Port, "status": http.StatusConflict})
		respondError(c, http.StatusConflict,
			"a box in phase "+string(b.Status)+" cannot publish a port; it must be Ready or Idle")
		return
	}

	started := time.Now()
	exp, err := stack.exposer.Expose(b.Name, req.Port)
	if err != nil {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{
			"reason": "expose_failed", "port": req.Port, "detail": err.Error(), "status": http.StatusInternalServerError,
		})
		respondError(c, http.StatusInternalServerError, "failed to publish the port: "+err.Error())
		return
	}
	// Measured to the first real 200 from the published address, which is why the
	// probe happens before the metric and before the response.
	probe := awaitPublishedURL(exp.URL, exp.Hostname, exposeProbeBudget, exposeProbeInterval)
	elapsed := time.Since(started)
	metrics.RecordBoxExpose("wildcard", elapsed)

	var exposureID uuid.UUID
	if err := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO box_exposures (box_id, port, hostname, url, cert)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (box_id, port) WHERE withdrawn_at IS NULL
		 DO UPDATE SET hostname = EXCLUDED.hostname, url = EXCLUDED.url, cert = EXCLUDED.cert
		 RETURNING id`,
		b.ID, req.Port, exp.Hostname, exp.URL, exp.Cert,
	).Scan(&exposureID); err != nil {
		audit(uuid.Nil, auditOutcomeFailure, map[string]any{"reason": "exposure_insert_failed", "port": req.Port, "status": http.StatusInternalServerError})
		respondError(c, http.StatusInternalServerError, "failed to record the exposure")
		return
	}

	audit(uuid.Nil, auditOutcomeSuccess, map[string]any{
		"exposure_id":  exposureID,
		"port":         req.Port,
		"hostname":     exp.Hostname,
		"cert":         exp.Cert,
		"answered":     probe.ok,
		"probe_status": probe.status,
		"expose_ms":    elapsed.Milliseconds(),
	})

	c.JSON(http.StatusOK, gin.H{
		"exposure": gin.H{
			"id":       exposureID,
			"port":     exp.Port,
			"hostname": exp.Hostname,
			"url":      exp.URL,
			"cert":     exp.Cert,
		},
		"first_response": gin.H{
			"status":   probe.status,
			"ok":       probe.ok,
			"body":     probe.body,
			"attempts": probe.attempts,
		},
		"expose_ms": elapsed.Milliseconds(),
		"note": "the hostname is assigned by the platform under its wildcard and cannot be chosen. " +
			"Responses carry X-Robots-Tag: noindex.",
	})
}

// An ingress object and its wildcard certificate are programmed by the edge a
// few seconds after the API returns, so a single immediate probe reports the
// gap rather than the outcome: callers saw ok=false with a certificate error on
// an address that answered 200 three seconds later. The budget is what an agent
// is willing to wait for its own URL, not a retry policy for a broken edge.
const (
	exposeProbeBudget   = 25 * time.Second
	exposeProbeInterval = 500 * time.Millisecond
)

type publishedProbe struct {
	status   int
	ok       bool
	body     string
	attempts int
}

// awaitPublishedURL probes the published address until it answers 200 or the
// budget runs out, and reports the last observation either way. A failure after
// the budget is a real failure: the caller waited as long as it declared it
// would and nothing answered.
func awaitPublishedURL(url, host string, budget, interval time.Duration) publishedProbe {
	deadline := time.Now().Add(budget)
	var last publishedProbe
	for attempt := 1; ; attempt++ {
		last = probePublishedURL(url, host)
		last.attempts = attempt
		if last.ok || !time.Now().Add(interval).Before(deadline) {
			return last
		}
		time.Sleep(interval)
	}
}

// probePublishedURL fetches the published address once, sending the assigned
// hostname as the Host header so the request is the one a real client makes.
func probePublishedURL(url, host string) publishedProbe {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return publishedProbe{body: err.Error()}
	}
	req.Host = host
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return publishedProbe{body: err.Error()}
	}
	defer resp.Body.Close()
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	return publishedProbe{status: resp.StatusCode, ok: resp.StatusCode == http.StatusOK, body: string(buf[:n])}
}

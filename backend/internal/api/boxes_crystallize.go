package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/dada-tuda/console/backend/internal/box"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// crystallize: promote the box onto a real VM, per ADR-019.
//
// The verification report is the deliverable, not the rsync. ADR-019 §7 is explicit
// about what counts: file-manifest equality on (path, size, mode, sha256), equality
// of the listening-socket set, and an end-to-end probe — never "rsync exited 0",
// because an rsync that copied nothing exits 0. The report is returned to the caller
// AND stored in box_crystallizations, because a manifest comparison nobody can
// re-read is a check that was performed and then thrown away.
//
// AckMonthlyCharge is a consent gate and not a flag. Promotion converts a
// per-minute body into a monthly VM bill, so the API answers 409 until the caller
// acknowledges it.
//
// Two facts the report and this handler keep visible rather than smoothing over:
// verification can FAIL while the operation itself completed, so `verified` is a
// separate column from `status`; and every carry disposition of "lost" increments
// dada_box_crystallize_state_loss_total, the only critical box alert, because one
// loss teaches distrust and severs the monetization ladder at step two.

type crystallizeBoxRequest struct {
	// AppServerName names the permanent artifact.
	AppServerName string `json:"app_server_name"`
	// Domain is the address the crystallized artifact answers on.
	Domain string `json:"domain"`
	// AckMonthlyCharge is consent, not a flag. Without it the answer is 409.
	AckMonthlyCharge bool `json:"ack_monthly_charge"`
	// ProbePath is the path the end-to-end probe requests.
	ProbePath string `json:"probe_path"`
}

// CrystallizeBox materializes the box's userland onto a permanent VM and verifies it.
//
// @ID          crystallizeBox
// @Summary     Crystallize a box into a permanent VM (ADR-019)
// @Description Materializes the box's USERLAND onto a real VM booted from a standard OS slug: the VM keeps its own kernel, init and bootloader, and only the application half of the filesystem is applied onto it. Named volumes are restored by mount path, env is written out of band at 0600 root, and the entrypoint and published ports become a systemd unit — the result runs under systemd with no Docker, no compose and no Portainer agent, because a crystallized VM is not a container host. Then it is VERIFIED: file-manifest equality on (path, size, mode, sha256), equality of the listening-socket set before the freeze and after the cutover, an sha256-per-key env comparison, and an end-to-end HTTP probe. Never "rsync exited 0" — an rsync that copied nothing exits 0. Requires ack_monthly_charge: promotion turns a per-minute body into a monthly VM bill, so consent is a gate and the answer is 409 without it. The full verification report, including the exclusion lists it applied, is returned and stored.
// @Tags        box
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                true "Project UUID"
// @Param       boxName   path     string                true "Box name"
// @Param       body      body     crystallizeBoxRequest true "Target name, domain and the monthly-charge acknowledgement"
// @Success     200       {object} map[string]interface{} "object with the verification report and the carry manifest"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string "the monthly charge was not acknowledged, or the box is not in a promotable phase"
// @Failure     422       {object} map[string]string "the materialization completed but verification failed; the report says which check"
// @Failure     503       {object} map[string]string "box runtime not configured"
// @Router      /projects/{projectId}/boxes/{boxName}/crystallize [post]
func (h *Handler) CrystallizeBox(c *gin.Context) {
	claims, projectID, ok := h.boxWriteGate(c, true)
	if !ok {
		return
	}
	boxName := c.Param("boxName")
	var envID uuid.UUID

	// Crystallization is the step that converts a per-minute box into a monthly
	// VM bill, so a refused promotion is a billing-relevant event, not just a
	// failed request. The consent gate in particular has to be visible: a
	// customer who reached the price and backed out looks identical to one who
	// never tried, and those are opposite product signals.
	reject := func(status int, reason string) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        models.ActionCrystallizeBox,
			ResourceKind:  models.ResourceKindBox,
			ResourceName:  boxName,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status},
		})
	}
	rejectErr := func(status int, reason, msg string) {
		reject(status, reason)
		respondError(c, status, msg)
	}

	stack, ok := h.requireBoxRuntime(c)
	if !ok {
		reject(c.Writer.Status(), "box_runtime_unavailable")
		return
	}
	local, ok := stack.requireLocalRuntime(c, "crystallization")
	if !ok {
		reject(c.Writer.Status(), "local_runtime_unavailable")
		return
	}
	b, ok := h.resolveBox(c, projectID, boxName)
	if !ok {
		reject(c.Writer.Status(), "box_not_found")
		return
	}
	envID = b.EnvironmentID
	var req crystallizeBoxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		rejectErr(http.StatusBadRequest, "malformed_body", err.Error())
		return
	}
	if !req.AckMonthlyCharge {
		// A consent gate, not a validation error: the caller is being told what the
		// action costs, not that they filled in a form wrong.
		rejectErr(http.StatusConflict, "monthly_charge_not_acknowledged",
			"crystallization converts a per-minute box into a monthly VM bill; set ack_monthly_charge=true to consent")
		return
	}
	name := req.AppServerName
	if name == "" {
		name = b.Name + "-vm"
	}
	if err := validateKubeName(name); err != nil {
		rejectErr(http.StatusBadRequest, "invalid_vm_name", err.Error())
		return
	}
	if !models.CanTransitionBoxStatus(b.Status, models.BoxStatusCrystallizing) {
		rejectErr(http.StatusConflict, "box_not_promotable",
			fmt.Sprintf("a box in phase %s cannot be crystallized", b.Status))
		return
	}
	if b.InstanceRef == "" {
		rejectErr(http.StatusConflict, "no_runtime_instance", "the box has no runtime instance to crystallize")
		return
	}
	domain := req.Domain
	if domain == "" {
		domain = name + "." + h.cfg.BoxCrystallizeDomainBase
	}

	var crystID uuid.UUID
	if err := h.pool.QueryRow(c.Request.Context(),
		`INSERT INTO box_crystallizations (box_id, environment_id, vm_name, domain, os_slug, status, stage, created_by)
		 VALUES ($1, $2, $3, $4, $5, 'Running', 'capture', $6)
		 RETURNING id`,
		b.ID, b.EnvironmentID, name, domain, box.WarmImageOSSlug, claims.UserID,
	).Scan(&crystID); err != nil {
		// The partial unique index refuses a second in-flight crystallization of the
		// same box: two promotions would race on the same VM root and the same ports,
		// and the DATABASE is what must refuse that rather than two API replicas.
		rejectErr(http.StatusConflict, "crystallization_already_in_flight",
			"a crystallization of this box is already in flight")
		return
	}

	if _, err := h.pool.Exec(c.Request.Context(),
		`UPDATE boxes SET status = 'Crystallizing', updated_at = now() WHERE id = $1`, b.ID); err != nil {
		rejectErr(http.StatusInternalServerError, "mark_crystallizing_failed", "failed to mark box crystallizing")
		return
	}

	inst := instanceFor(b.ID.String(), b.InstanceRef, b.NodeRef, b.Image, b.Region, b.SSHHost, b.SSHPort, b.MCPURL)
	cz := &box.LocalCrystallizer{Runtime: local, Clock: box.SystemClock{}}
	report, cErr := cz.CrystallizeWithReport(c.Request.Context(), inst, box.CrystallizeOptions{
		VMName:    name,
		Domain:    domain,
		OSSlug:    box.WarmImageOSSlug,
		ProbePath: req.ProbePath,
	})

	verified := cErr == nil
	status := "Verified"
	errMsg := ""
	if !verified {
		status = "Failed"
		errMsg = cErr.Error()
	}
	reportJSON, _ := json.Marshal(report)
	var carryJSON []byte
	if report != nil {
		carryJSON, _ = json.Marshal(report.Carry)
	}
	stage, durationMS := "unknown", int64(0)
	if report != nil {
		stage, durationMS = report.Stage, report.DurationMS
	}
	if _, err := h.pool.Exec(c.Request.Context(),
		`UPDATE box_crystallizations
		    SET status = $2, stage = $3, verified = $4, error_message = $5,
		        report = $6, carry = $7, duration_ms = $8, finished_at = now()
		  WHERE id = $1`,
		crystID, status, stage, verified, errMsg, reportJSON, carryJSON, durationMS,
	); err != nil {
		rejectErr(http.StatusInternalServerError, "report_store_failed", "failed to store the verification report")
		return
	}

	// The box goes back to Ready either way. ADR-019's storage reaper keeps the box
	// for 72 hours after a promotion precisely so a customer who finds something
	// missing still has the original — and a FAILED verification is the case where
	// that matters most, so the box must not be torn down on failure.
	if _, err := h.pool.Exec(c.Request.Context(),
		`UPDATE boxes SET status = 'Ready', error_message = $2, updated_at = now() WHERE id = $1`,
		b.ID, errMsg,
	); err != nil {
		rejectErr(http.StatusInternalServerError, "return_to_ready_failed", "failed to return the box to Ready")
		return
	}

	// An unverified promotion answers 422 and leaves the customer with a VM that
	// did not prove itself. Recording it as a plain success -- which is what the
	// raw insert did -- makes exactly the case worth reviewing the one that reads
	// as fine.
	outcome := auditOutcomeSuccess
	if !verified {
		outcome = auditOutcomeFailure
	}
	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        models.ActionCrystallizeBox,
		ResourceKind:  models.ResourceKindBox,
		ResourceName:  b.Name,
		Outcome:       outcome,
		Metadata: map[string]any{
			"box_id": b.ID, "vm_name": name, "domain": domain,
			"verified": verified, "stage": stage, "duration_ms": durationMS,
			"crystallization_id": crystID,
		},
	})

	body := gin.H{
		"crystallization_id": crystID,
		"verified":           verified,
		"report":             report,
		// The human-readable report is returned alongside the structured one because
		// the exclusion lists have to be READ by a person: ADR-019 requires the list
		// to be part of the report, on the grounds that a list nobody sees diverges
		// from reality without anyone noticing.
		"report_text": reportText(report),
	}
	if !verified {
		body["error"] = errMsg
		body["message"] = "materialization completed but verification FAILED; the box is kept so nothing is lost"
		c.JSON(http.StatusUnprocessableEntity, body)
		return
	}
	body["message"] = "crystallized and verified"
	c.JSON(http.StatusOK, body)
}

func reportText(r *box.CrystallizationReport) string {
	if r == nil {
		return "no report: crystallization failed before one could be produced"
	}
	return r.Text()
}

// ListBoxCrystallizations returns a box's promotion attempts and their reports.
//
// @ID          listBoxCrystallizations
// @Summary     List a box's crystallization attempts
// @Description Returns every crystallization attempt for the box with its stored verification report. `verified` is separate from `status` on purpose: an attempt can finish while verification failed, and a dashboard that could not tell "finished" from "correct" would make the only critical box alert unreadable. Read-only.
// @Tags        box
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       boxName   path     string true "Box name"
// @Success     200       {object} map[string]interface{} "object with a crystallizations array"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/boxes/{boxName}/crystallizations [get]
func (h *Handler) ListBoxCrystallizations(c *gin.Context) {
	_, projectID, ok := h.boxWriteGate(c, false)
	if !ok {
		return
	}
	b, ok := h.resolveBox(c, projectID, c.Param("boxName"))
	if !ok {
		return
	}
	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT id, vm_name, domain, os_slug, status, stage, verified, error_message, report, carry, duration_ms, created_at, finished_at
		   FROM box_crystallizations WHERE box_id = $1 ORDER BY created_at DESC`, b.ID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query crystallizations")
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var (
			id                                          uuid.UUID
			vmName, domain, osSlug, status, stage, eMsg string
			verified                                    bool
			report, carry                               []byte
			durationMS                                  int64
			createdAt                                   any
			finishedAt                                  any
		)
		if err := rows.Scan(&id, &vmName, &domain, &osSlug, &status, &stage, &verified, &eMsg,
			&report, &carry, &durationMS, &createdAt, &finishedAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan crystallization")
			return
		}
		item := gin.H{
			"id": id, "vm_name": vmName, "domain": domain, "os_slug": osSlug,
			"status": status, "stage": stage, "verified": verified,
			"duration_ms": durationMS, "created_at": createdAt, "finished_at": finishedAt,
		}
		if eMsg != "" {
			item["error_message"] = eMsg
		}
		if len(report) > 0 {
			item["report"] = json.RawMessage(report)
		}
		if len(carry) > 0 {
			item["carry"] = json.RawMessage(carry)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading crystallizations")
		return
	}
	c.JSON(http.StatusOK, gin.H{"crystallizations": out})
}

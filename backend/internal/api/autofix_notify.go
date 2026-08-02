package api

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dada-tuda/console/backend/internal/notify"
)

const autofixNotifyTimeout = 30 * time.Second

// isTerminalTaskStatus reports whether a cloud_tasks status is an end state.
func isTerminalTaskStatus(status string) bool {
	return status == "completed" || status == "failed" || status == "canceled"
}

// shouldNotifyAutofix reports whether one row update is the moment to send an
// outcome email: an auto-fix task that just crossed into a terminal status.
// A row that was already terminal is a repeat callback and must stay silent.
func shouldNotifyAutofix(tr cloudTaskTransition) bool {
	return tr.Matched && tr.TaskType == "autofix" &&
		isTerminalTaskStatus(tr.NewStatus) && !isTerminalTaskStatus(tr.OldStatus)
}

// notifyAutofixOutcome closes the auto-fix loop: it mails the app owner when a
// run leaves a pull request waiting, and the operator when a run dies.
//
// This is the missing half of the feature as it shipped. Prod evidence: nine
// runs, six of them failed on infrastructure with nobody told, and of the three
// that opened a PR none was merged -- the link existed only in a cloud_tasks
// row. A fix nobody hears about is worth exactly as much as no fix.
//
// Only a transition INTO a terminal status notifies, so the repeat callbacks
// the agent sends for an already-finished run cannot mail twice.
func (h *Handler) notifyAutofixOutcome(tr cloudTaskTransition) {
	if !shouldNotifyAutofix(tr) || h.auditNotifier == nil {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), autofixNotifyTimeout)
		defer cancel()
		h.sendAutofixOutcome(ctx, tr)
		h.settleFeedbackForTask(ctx, tr)
	}()
}

// sendAutofixOutcome picks the recipient and the message for one finished run.
// A run that reports success without a pull request is treated as a failure and
// routed to the operator: it produced nothing the owner can act on, and it is
// exactly the shape of the silent infrastructure deaths seen on prod.
func (h *Handler) sendAutofixOutcome(ctx context.Context, tr cloudTaskTransition) {
	consoleLink := fmt.Sprintf("%s/projects/%s/apps/%s", h.cfg.PublicBaseURL, tr.ProjectID, tr.AppName)

	if tr.NewStatus == "completed" && tr.PRURL != "" {
		to, source := h.resolveAlertRecipient(ctx, tr.ProjectID)
		if to == "" {
			to = h.auditNotifyEmail
			source = alertSourceOperator
		}
		if to == "" {
			log.Printf("autofix: no recipient for project %s, PR %s for app=%s goes unannounced", tr.ProjectID, tr.PRURL, tr.AppName)
			return
		}
		subject, body := notify.ComposeAutofixReady(tr.AppName, tr.PRURL, consoleLink)
		if source == alertSourceOperator {
			subject, body = notify.ComposeNoOwnerFallback(tr.ProjectID.String(), h.projectDisplayName(ctx, tr.ProjectID), subject, body)
		}
		if err := h.auditNotifier.Send(to, subject, body); err != nil {
			log.Printf("autofix: PR notice to %s for app=%s failed: %v", to, tr.AppName, err)
			h.recordNotifySend(ctx, tr.ProjectID, "AutofixReady", tr.AppName, source, err)
			return
		}
		h.recordNotifySend(ctx, tr.ProjectID, "AutofixReady", tr.AppName, source, nil)
		log.Printf("autofix: told %s (source=%s) about PR %s for app=%s", to, source, tr.PRURL, tr.AppName)
		return
	}

	if h.auditNotifyEmail == "" {
		return
	}
	reason := tr.Error
	if reason == "" && tr.NewStatus == "completed" {
		reason = "run reported success but opened no pull request"
	}
	if reason == "" {
		reason = tr.NewStatus
	}
	subject, body := notify.ComposeAutofixFailed(tr.AppName, reason, consoleLink)
	if err := h.auditNotifier.Send(h.auditNotifyEmail, subject, body); err != nil {
		log.Printf("autofix: failure notice for app=%s to %s failed: %v", tr.AppName, h.auditNotifyEmail, err)
		h.recordNotifySend(ctx, tr.ProjectID, "AutofixFailed", tr.AppName, alertSourceOperator, err)
		return
	}
	h.recordNotifySend(ctx, tr.ProjectID, "AutofixFailed", tr.AppName, alertSourceOperator, nil)
}

// settleFeedbackForTask moves a support ticket on when the auto-fix run it
// launched ends. A failed run puts the ticket back in the queue rather than
// leaving it parked in in_progress forever, because the customer is still
// waiting and no human has looked at it yet.
func (h *Handler) settleFeedbackForTask(ctx context.Context, tr cloudTaskTransition) {
	status := "new"
	resolution := "auto-fix failed: " + tr.Error
	if tr.NewStatus == "completed" && tr.PRURL != "" {
		status = "in_progress"
		resolution = "auto-fix PR: " + tr.PRURL
	}
	if _, err := h.pool.Exec(ctx,
		`UPDATE feedback SET status=$2, resolution=$3 WHERE cloud_task_id=$1 AND status <> 'resolved'`,
		tr.ID, status, resolution); err != nil {
		log.Printf("autofix: settling feedback for cloud task %s failed: %v", tr.ID, err)
	}
}

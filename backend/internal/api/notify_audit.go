package api

import (
	"context"

	"github.com/google/uuid"
)

// notifySendErrorMaxLen bounds the transport error stored in audit metadata.
// A rune-safe cut: slicing bytes can land inside a multi-byte rune and hand
// Postgres invalid UTF-8, which would fail the very row meant to record the
// failure.
const notifySendErrorMaxLen = 300

// recordNotifySend writes the outcome of one outbound notification.
//
// Every alerting path in the platform used to end the same way: Send returns an
// error, the error goes to a log line, and the pod that wrote it rotates. The
// question "did this app owner actually get told" then has no answer at all --
// not in the console, not in the audit journal, not anywhere a support reply
// could cite. app_health_alerts got per-row send columns for exactly this
// reason; the other alerting paths have no such row, so their answer lives here
// instead, in the one table that is already the record of what happened.
//
// kind names the notification (VolumeAlert, AutoscaleCeiling, AutofixReady,
// AutofixFailed), NOT the message: subjects and bodies carry app logs, error
// text and links, and audit_events is not where those belong.
//
// The recipient address never enters metadata either -- 152-FZ. What goes in is
// how the recipient was chosen (project owner, member, personal org, operator
// fallback), which is what makes a row readable: "the owner was told" and
// "nobody was reachable so we told ourselves" are different outcomes, and the
// second one is the interesting one.
func (h *Handler) recordNotifySend(ctx context.Context, projectID uuid.UUID, kind, appName, recipientSource string, sendErr error) {
	meta := map[string]any{
		"kind":             kind,
		"recipient_source": recipientSource,
		"app":              appName,
	}
	outcome := auditOutcomeSuccess
	if sendErr != nil {
		outcome = auditOutcomeFailure
		errText := sendErr.Error()
		if runes := []rune(errText); len(runes) > notifySendErrorMaxLen {
			errText = string(runes[:notifySendErrorMaxLen])
		}
		meta["reason"] = "send_failed"
		meta["detail"] = errText
	}
	h.recordSystemAudit(ctx, auditEntry{
		ProjectID:    projectID,
		Action:       "SendNotification",
		ResourceKind: "Notification",
		ResourceName: kind,
		Outcome:      outcome,
		Metadata:     meta,
	})
}

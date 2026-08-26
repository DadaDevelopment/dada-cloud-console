package api

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/supporttask"
	"github.com/google/uuid"
)

const supportIntakeTimeout = 15 * time.Second

// dispatchSupportIntake files a new ticket onto the AgentSyncHub kanban.
// Fire-and-forget by the same rule as notifyFeedback: an AgentSyncHub outage
// must never turn a customer's successful submit into an error, and the
// feedback row (already committed by the time this runs) remains the source
// of truth the user's "my tickets" view reads regardless of whether this
// call ever lands. Best-effort, but every attempt and its outcome is logged
// so a stuck ticket is at least discoverable in the pod log, not silent.
//
// projectKey is intentionally omitted here: dada-cloud has no notion of the
// AgentSyncHub project key namespace (only project UUID + app name), so
// sending a wrong guess would misfile the card. requester carries the
// sender's email when known so a card is attributable without becoming a
// second identity system.
func (h *Handler) dispatchSupportIntake(feedbackID uuid.UUID, message, senderEmail, appName string) {
	if h.supportTask == nil {
		return
	}
	client := h.supportTask
	title := supportIntakeTitle(message)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), supportIntakeTimeout)
		defer cancel()
		res, err := client.Intake(ctx, supporttask.Request{
			SupportTaskID: feedbackID.String(),
			Title:         title,
			Report:        message,
			Requester:     senderEmail,
			AppName:       appName,
		})
		if err != nil {
			log.Printf("feedback: support intake for %s failed: %v", feedbackID, err)
			return
		}
		if _, err := h.pool.Exec(ctx,
			`UPDATE feedback SET support_task_id=$2 WHERE id=$1 AND support_task_id=''`,
			feedbackID, res.ID); err != nil {
			log.Printf("feedback: recording support_task_id for %s failed: %v", feedbackID, err)
		}
	}()
}

const supportIntakeTitleMaxLen = 120

// supportIntakeTitle derives a bounded kanban card title from a ticket's
// first line, since the intake contract has no separate subject field on the
// wire the console collects. The result is always trimmed and never blank:
// a whitespace-only first line (e.g. a message that opens with a blank line)
// must still fall back to a placeholder, not file a blank-titled card.
func supportIntakeTitle(message string) string {
	title := message
	for i, r := range message {
		if r == '\n' {
			title = message[:i]
			break
		}
	}
	title = strings.TrimSpace(title)
	if len(title) > supportIntakeTitleMaxLen {
		title = title[:supportIntakeTitleMaxLen]
	}
	if title == "" {
		title = "Support ticket"
	}
	return title
}

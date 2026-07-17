package api

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/google/uuid"
)

// significantAuditActions is the curated set of audit_events.action values that
// warrant an owner email. Deliberately excludes reads/list/status-poll actions
// and anything not tied to a durable create/connect/delete of a resource.
var significantAuditActions = map[string]bool{
	"CreateApp":             true,
	"CreateProject":         true,
	"CreateServiceDatabase": true,
	"ConnectGitRepo":        true,
	"TriggerBuild":          true,
	"AttachCustomHostname":  true,
	"DeleteApp":             true,
}

// auditNotifyWindow/auditNotifyBurst bound the owner's inbox during a storm
// (bulk scripted actions, a bad CI loop): once burst emails have gone out in
// one window, further significant events in that window are log-only.
const (
	auditNotifyWindow = 5 * time.Minute
	auditNotifyBurst  = 10
)

// auditNotifyLimiter is a cheap per-pod fixed-window counter. It does not need
// to be exact or shared across pods — it only exists to stop one pod's inbox
// storm, not to enforce a global quota.
type auditNotifyLimiter struct {
	mu          sync.Mutex
	windowStart time.Time
	count       int
}

func (l *auditNotifyLimiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.windowStart) > auditNotifyWindow {
		l.windowStart = now
		l.count = 0
	}
	l.count++
	return l.count <= auditNotifyBurst
}

// isServiceAccountUsername reuses the signup-notify convention: Keycloak
// client-credentials principals are named service-account-<clientId> and must
// never trigger an owner email (they run at CI/agent volume, not human pace).
func isServiceAccountUsername(username string) bool {
	return strings.HasPrefix(username, serviceAccountUsernamePrefix)
}

// notifyAuditEvent fires the owner email for one significant audit_events row,
// off the request's hot path. No-op when the notifier/recipient are unset, the
// action isn't in the curated set, the actor is a service account, or the
// per-pod rate limiter is currently tripped. Every failure is logged and
// swallowed — audit writes and the underlying request must never be affected
// by a mail outage.
func (h *Handler) notifyAuditEvent(claims *auth.Claims, projectID uuid.UUID, action, resourceName string) {
	if h.auditNotifier == nil || h.auditNotifyEmail == "" || claims == nil {
		return
	}
	if !significantAuditActions[action] {
		return
	}
	if isServiceAccountUsername(claims.Username) {
		return
	}
	if !h.auditRateLimiter.allow() {
		log.Printf("audit notify: rate limit exceeded (%d/%s), dropping email for action=%s resource=%s", auditNotifyBurst, auditNotifyWindow, action, resourceName)
		return
	}

	actorEmail := claims.Email
	if actorEmail == "" {
		actorEmail = claims.Username
	}
	createdAtUTC := time.Now().UTC().Format(time.RFC3339)
	pool := h.pool
	notifier := h.auditNotifier
	to := h.auditNotifyEmail

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		projectName := projectID.String()
		var name string
		if err := pool.QueryRow(ctx, "SELECT name FROM projects WHERE id = $1", projectID).Scan(&name); err == nil && name != "" {
			projectName = name
		}

		subject, body := notify.ComposeAudit(action, actorEmail, resourceName, projectName, createdAtUTC)
		if err := notifier.Send(to, subject, body); err != nil {
			log.Printf("audit notify: send to %s failed: %v", to, err)
		}
	}()
}

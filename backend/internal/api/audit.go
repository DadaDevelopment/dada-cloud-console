package api

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Audit actions for the passive steps of a user path. Until these existed the
// path analysis could not distinguish "gave up waiting for the build" from
// "saw green and left": audit_events only ever held successful write-actions.
const (
	auditActionSessionStart  = "SessionStart"
	auditActionViewBuildLogs = "ViewBuildLogs"
	auditActionViewAppLogs   = "ViewAppLogs"
	auditActionViewProject   = "ViewProject"
	auditActionViewApp       = "ViewApp"
)

const (
	auditOutcomeSuccess = "success"
	auditOutcomeFailure = "failure"
)

// Dedupe windows. Both passive signals are emitted from endpoints the console
// polls, so without collapsing they would drown the write-actions they are
// meant to contextualize.
//
// auditSessionIdleGap is a VISIT boundary, not a rate limit: a visit ends when
// the user stops making requests for this long. A fixed "one row per window"
// collapse counted two distinct visits as one whenever they fell inside the
// same window, which made the return rate — the number the path analysis is
// built on — silently too low.
const (
	auditSessionIdleGap = 30 * time.Minute
	auditViewWindow     = 10 * time.Minute
)

// auditSeenLimit caps the tracker so a token-storm cannot grow it without
// bound; on overflow the whole map is dropped (worst case: a few duplicate
// rows, never a missing one).
const auditSeenLimit = 10000

// auditSeen is a per-pod first-seen-in-window tracker. Exactness across pods is
// not required: a duplicate SessionStart row from a second pod is harmless for
// path analysis, a missing one is not, so the bias is deliberately toward
// recording.
type auditSeen struct {
	mu     sync.Mutex
	window time.Duration
	seen   map[string]time.Time
}

func newAuditSeen(window time.Duration) *auditSeen {
	return &auditSeen{window: window, seen: make(map[string]time.Time)}
}

// allow reports whether key should be recorded now, and marks it seen.
func (s *auditSeen) allow(key string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.seen[key]; ok && now.Sub(last) < s.window {
		return false
	}
	if len(s.seen) >= auditSeenLimit {
		s.seen = make(map[string]time.Time)
	}
	s.seen[key] = now
	return true
}

// auditVisitState is the last thing seen for one user on this pod: when they
// last made any authenticated request, and which Keycloak session that request
// belonged to.
type auditVisitState struct {
	lastSeen time.Time
	sid      string
}

// auditVisits decides whether an authenticated request opens a NEW visit.
// Unlike auditSeen it refreshes lastSeen on every request, so a long session
// stays one visit while a real gap — or a re-login, which yields a new Keycloak
// sid — starts another.
type auditVisits struct {
	mu      sync.Mutex
	idleGap time.Duration
	users   map[string]auditVisitState
}

func newAuditVisits(idleGap time.Duration) *auditVisits {
	return &auditVisits{idleGap: idleGap, users: make(map[string]auditVisitState)}
}

// observe records the request and reports whether it starts a new visit,
// together with the reason ("first", "idle", "relogin") for the audit metadata.
func (v *auditVisits) observe(userID, sid string, now time.Time) (bool, string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	prev, known := v.users[userID]
	if len(v.users) >= auditSeenLimit {
		v.users = make(map[string]auditVisitState)
		known = false
	}
	v.users[userID] = auditVisitState{lastSeen: now, sid: sid}

	switch {
	case !known:
		return true, "first"
	case now.Sub(prev.lastSeen) >= v.idleGap:
		return true, "idle"
	case sid != "" && prev.sid != "" && sid != prev.sid:
		return true, "relogin"
	}
	return false, ""
}

var (
	auditSessionVisits = newAuditVisits(auditSessionIdleGap)
	auditViewSeen      = newAuditSeen(auditViewWindow)
)

// auditEntry is the full shape of an audit_events row. ProjectID/EnvironmentID/
// OperationID are optional (uuid.Nil writes NULL) because session-level events
// belong to no project.
type auditEntry struct {
	ProjectID     uuid.UUID
	EnvironmentID uuid.UUID
	OperationID   uuid.UUID
	Action        string
	ResourceKind  string
	ResourceName  string
	Outcome       string
	Metadata      any
}

func nullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// recordAudit writes one audit row best-effort: an audit failure must never
// change the outcome of the request that triggered it.
func (h *Handler) recordAudit(ctx context.Context, actorID uuid.UUID, e auditEntry) {
	if h.pool == nil || actorID == uuid.Nil || e.Action == "" {
		return
	}
	outcome := e.Outcome
	if outcome == "" {
		outcome = auditOutcomeSuccess
	}
	meta := []byte("{}")
	if e.Metadata != nil {
		if b, err := json.Marshal(e.Metadata); err == nil {
			meta = b
		}
	}
	projectID, environmentID, operationID := e.ProjectID, e.EnvironmentID, e.OperationID
	unresolved := map[string]string{}

	for attempt := 0; attempt < 4; attempt++ {
		payload := meta
		if len(unresolved) > 0 {
			payload = mergeAuditMetadata(meta, unresolved)
		}
		_, err := h.pool.Exec(ctx,
			`INSERT INTO audit_events
			   (actor_id, project_id, environment_id, operation_id, action, resource_kind, resource_name, outcome, metadata)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			actorID, nullableUUID(projectID), nullableUUID(environmentID), nullableUUID(operationID),
			e.Action, e.ResourceKind, e.ResourceName, outcome, payload,
		)
		if err == nil {
			return
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != pgForeignKeyViolation {
			return
		}
		switch pgErr.ConstraintName {
		case "audit_events_project_id_fkey":
			unresolved["unresolved_project_id"] = projectID.String()
			projectID = uuid.Nil
		case "audit_events_environment_id_fkey":
			unresolved["unresolved_environment_id"] = environmentID.String()
			environmentID = uuid.Nil
		case "audit_events_operation_id_fkey":
			unresolved["unresolved_operation_id"] = operationID.String()
			operationID = uuid.Nil
		default:
			return
		}
	}
}

// pgForeignKeyViolation is PostgreSQL's SQLSTATE for a violated foreign key.
const pgForeignKeyViolation = "23503"

// mergeAuditMetadata folds the unresolved-reference notes into an already
// marshalled metadata object, so the id that could not be stored in its column
// is still readable on the row.
func mergeAuditMetadata(meta []byte, extra map[string]string) []byte {
	merged := map[string]any{}
	if err := json.Unmarshal(meta, &merged); err != nil {
		merged = map[string]any{}
	}
	for k, v := range extra {
		merged[k] = v
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return meta
	}
	return out
}

// recordAuditAsync writes the row off the request's hot path with its own
// deadline, because the request context is cancelled as soon as the response is
// flushed and passive signals are recorded during, not before, the response.
func (h *Handler) recordAuditAsync(actorID uuid.UUID, e auditEntry) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.recordAudit(ctx, actorID, e)
	}()
}

// recordViewAudit records a passive view (build logs, app logs), collapsed to
// one row per user+resource per auditViewWindow so console polling does not
// flood the table.
func (h *Handler) recordViewAudit(claims *auth.Claims, action string, e auditEntry) {
	if claims == nil || isServiceAccountUsername(claims.Username) {
		return
	}
	key := claims.UserID.String() + "|" + action + "|" + e.ResourceName
	if !auditViewSeen.allow(key, time.Now()) {
		return
	}
	e.Action = action
	h.recordAuditAsync(claims.UserID, e)
}

// auditSessionMiddleware emits one SessionStart per VISIT on any authenticated
// request. Keycloak has the login event, we do not — and without a session
// marker the gap between "registered" and "first write action" is a measurement
// of nothing: a user who logged in, looked around and left is indistinguishable
// from one who never came back.
func (h *Handler) auditSessionMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := auth.GetClaims(c)
		if ok && claims != nil && claims.UserID != uuid.Nil && !isServiceAccountUsername(claims.Username) {
			if newVisit, reason := auditSessionVisits.observe(claims.UserID.String(), claims.SessionID, time.Now()); newVisit {
				h.recordAuditAsync(claims.UserID, auditEntry{
					Action:       auditActionSessionStart,
					ResourceKind: "Session",
					ResourceName: claims.Username,
					Metadata:     map[string]any{"path": c.FullPath(), "visit": reason},
				})
			}
		}
		c.Next()
	}
}

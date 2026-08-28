package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/metrics"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
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
	auditActionViewApps      = "ViewApps"
	auditActionAutoscaleApp  = "AutoscaleApp"
)

// Audit actions for reads of a secret and for the box lifecycle steps that had
// no row at all. A secret handed to a caller is the one read that must leave a
// trace whether it succeeded or not: "who saw this value, and when" is
// unanswerable after the fact otherwise, and a run of refusals against one key
// is the shape a probe makes.
const (
	auditActionRevealEnvVar   = "RevealEnvVar"
	auditActionRevealModelKey = "RevealAIModelAPIKey"
	auditActionRevealDBCreds  = "RevealDatabaseCredentials"
	auditActionExtendBox      = "ExtendBox"
	auditActionSetAIRouting   = "SetAIRoutingMode"
)

// The two halves of the GitHub App install flight. The user leaves our origin
// for github.com between them, so this is the one step of the connect path we
// cannot observe from a single request: the intent row is written when the
// install URL is handed out, the verdict row when GitHub redirects back.
//
// They exist because the step was previously silent in both directions. A user
// who clicked "Connect GitHub" and never returned produced no row at all, which
// made "left for GitHub and did not come back" indistinguishable from "never
// got that far" -- and the drop is the largest measured leak on the path from
// signup to a live app. Start-minus-Finish over a window IS the mortality of
// the flight; the verdict's metadata.reason says which way it died.
const (
	auditActionStartGitAppInstall  = "StartGitAppInstall"
	auditActionFinishGitAppInstall = "FinishGitAppInstall"
)

// The two halves of one auto-fix run, same shape as the GitHub install flight
// above: TriggerAutofix only proves the run was launched (the agent accepted
// the job and did not throw), never whether it fixed anything, because the
// agent reports back later through its own webhook. ResolveAutofix is written
// once that webhook lands a terminal status, and its outcome is the one that
// answers "did this help" -- pending in between means exactly that: launched,
// not yet resolved. Splitting these was the fix for the
// lifecoachrussia@yandex.ru incident (2026-08-19): TriggerAutofix's launch
// audit read as "success" and nobody ever wrote a row for the fact that no
// pull request followed and the app stayed crash-looping.
const auditActionResolveAutofix = "ResolveAutofix"

// PlatformSelfHealRebuild is written twice per app the sweeper acts on, same
// intent/verdict split as the pair above: once with outcome=pending before
// the rebuild is queued (metadata carries the cause_kind and the registry
// note so the row is self-explanatory without a code lookup), and once more
// with the real outcome once the INSERT INTO builds has actually run. The
// two rows share OperationID, which is what makes them joinable -- see
// platform_selfheal.go.
const auditActionPlatformSelfHealRebuild = "PlatformSelfHealRebuild"

const (
	auditOutcomeSuccess = "success"
	auditOutcomeFailure = "failure"
	auditOutcomePending = "pending"
)

// The closed set audit_events.actor_type can hold (migration
// 124_audit_events_actor_type.sql). Every writer decides this at the point of
// insert -- see writeAuditRow -- instead of a reader guessing it later from
// actor_id. auditActorTypeService is reserved for a future machine-to-machine
// actor that is neither a person nor systemDeployActorID; nothing writes it
// yet.
const (
	auditActorTypeUser    = "user"
	auditActorTypeSystem  = "system"
	auditActorTypeService = "service"
)

// The two request headers a caller may use to self-report what it is. Neither
// is a fact: a script can send X-Dada-Client: cli/9.9.9 with no CLI anywhere
// near it. That is exactly why the audit fields they feed are named
// "*_claimed" (see clientClaim below) instead of something that reads as
// verified, like "client" or "source".
const (
	headerClientClaimed       = "X-Dada-Client"
	headerAgentSessionClaimed = "X-Dada-Agent-Session"
)

// The closed set client_claimed can hold. Chosen from what actually calls the
// authenticated API today, plus the one gap ddc deploy is closing:
//   - ui: the console web app. No caller sends this header yet, so it is also
//     the default for a request with none -- every deploy before the CLI
//     shipped came from the browser, and that history must not turn into a
//     wall of "unknown".
//   - cli: the ddc deploy tool this instrumentation exists for.
//   - api: a bearer-token caller that is not the console and not the CLI --
//     a hand-rolled script against the documented API, or the embedded MCP
//     self-proxy on behalf of an assistant tool call.
//   - webhook: reserved for a self-declared inbound integration built on top
//     of a user's own bearer token (e.g. a third-party automation platform).
//     The two actual webhook routes (/api/v1/deploy, /api/v1/webhooks/*) sit
//     outside the authenticated group this header is read in, so they do not
//     go through this classifier at all; the value stays here for a caller
//     that legitimately is one.
//   - unknown: the header was present but did not parse as one of the above --
//     garbage, a typo, or a future client class this list has not caught up
//     with yet. Never store the raw text: an open text field is how an audit
//     column turns into a dumping ground for whatever any caller feels like
//     sending.
const (
	clientClaimedUI      = "ui"
	clientClaimedCLI     = "cli"
	clientClaimedAPI     = "api"
	clientClaimedWebhook = "webhook"
	clientClaimedUnknown = "unknown"
)

// clientClaimedRe is the allowlist a claimed X-Dada-Client value must satisfy:
// one of the four known classes, optionally followed by "/<version>" where
// version is a short, plain token. Anything else -- wrong shape, embedded
// separators, an essay -- classifies as unknown rather than being stored
// verbatim.
var clientClaimedRe = regexp.MustCompile(`^(ui|cli|api|webhook)(/[a-zA-Z0-9][a-zA-Z0-9._-]{0,31})?$`)

// classifyClientClaimed turns a raw X-Dada-Client header into one of the
// closed client_claimed values. Empty (the header was never sent) is
// deliberately UI, not unknown: see the const block above.
func classifyClientClaimed(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return clientClaimedUI
	}
	if len(raw) > 40 {
		return clientClaimedUnknown
	}
	m := clientClaimedRe.FindStringSubmatch(strings.ToLower(raw))
	if m == nil {
		return clientClaimedUnknown
	}
	return m[1]
}

// agentSessionClaimedRe bounds the shape of a claimed agent-session marker:
// short, plain-token, no free text. It is deliberately generous about content
// (a UUID, a hash prefix, a short slug are all fine) and strict about shape,
// because the field only needs to answer "was this call made on behalf of an
// agent session", not to identify which one.
var agentSessionClaimedRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,63}$`)

// classifyAgentSessionClaimed turns a raw X-Dada-Agent-Session header into a
// value safe to store, or "" when the header was not sent at all -- absent
// and unparseable are kept distinguishable: "" means no marker was claimed,
// clientClaimedUnknown means one was claimed but did not fit the allowlist.
func classifyAgentSessionClaimed(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if len(raw) > 64 || !agentSessionClaimedRe.MatchString(raw) {
		return clientClaimedUnknown
	}
	return raw
}

// auditClientMetaKey/auditAgentSessionMetaKey are the metadata keys
// writeAuditRow injects. Named "*_claimed", matching the const/header naming
// above, so a reader of audit_events sees at a glance that these came from
// the caller's own self-report and were never independently verified.
const (
	auditClientMetaKey       = "client_claimed"
	auditAgentSessionMetaKey = "agent_session_claimed"
)

// clientClaimContextKey is unexported so no other package can collide with it
// by accident.
type clientClaimContextKey struct{}

// clientClaim is the pair of self-reported values extracted once per request
// by clientClaimMiddleware, and read back by writeAuditRow -- the single
// place every audit row of every shape passes through -- so instrumenting a
// new deploy path never again means copy-pasting header parsing into a
// handler.
type clientClaim struct {
	client       string
	agentSession string
}

// clientClaimMiddleware reads the two self-report headers once per request
// and stores the classified result on the request context. It is mounted on
// the authenticated /api/v1 group only: the two actual webhook routes
// (deploy-hook consumption, YooKassa, etc.) are public routes outside that
// group and are not meant to default to "ui".
func clientClaimMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		claim := clientClaim{
			client:       classifyClientClaimed(c.GetHeader(headerClientClaimed)),
			agentSession: classifyAgentSessionClaimed(c.GetHeader(headerAgentSessionClaimed)),
		}
		ctx := context.WithValue(c.Request.Context(), clientClaimContextKey{}, claim)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// clientClaimFromContext reads back what clientClaimMiddleware stored. The ok
// result is false for any request that never passed through that middleware
// (background sweepers, the two async audit paths that hand the write a fresh
// context.Background()) -- writeAuditRow leaves those rows exactly as they
// were before this instrumentation existed.
func clientClaimFromContext(ctx context.Context) (clientClaim, bool) {
	claim, ok := ctx.Value(clientClaimContextKey{}).(clientClaim)
	return claim, ok
}

// withClientClaimMetadata folds the classified client_claimed/
// agent_session_claimed pair into an already-marshalled metadata object,
// without overwriting a key a caller set explicitly on auditEntry.Metadata.
// agent_session_claimed is omitted entirely when no marker was claimed, so
// "field absent" and "field claimed but unparseable (unknown)" stay
// distinguishable in the stored JSON.
func withClientClaimMetadata(meta []byte, claim clientClaim) []byte {
	merged := map[string]any{}
	if err := json.Unmarshal(meta, &merged); err != nil || merged == nil {
		merged = map[string]any{}
	}
	if _, exists := merged[auditClientMetaKey]; !exists {
		merged[auditClientMetaKey] = claim.client
	}
	if claim.agentSession != "" {
		if _, exists := merged[auditAgentSessionMetaKey]; !exists {
			merged[auditAgentSessionMetaKey] = claim.agentSession
		}
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return meta
	}
	return out
}

// auditInsertSQL writes the row a handler asked for, except that a claimed
// success on an operation that has not finished yet is stored as pending.
//
// Every handler audit row is written at enqueue time: the transaction that
// inserts the operation with status Created commits, and the audit row follows
// immediately, with no outcome set and therefore success. Nothing ever updates
// it -- audit_events is append-only -- so an action that is merely accepted and
// then fails, or never deploys at all, stays success forever. Activation
// measured off outcome='success' counts clicks, not deploys.
//
// The verdict is written later as a second row against the same operation:
// success from MarkCommitted/MarkReady in the agents, failure from
// recordOperationFailureAudit. The pair reads as intent then result.
//
// The test is on the operation's status, not on a list of actions, so a caller
// that starts writing terminal rows is not silently mislabelled: the caller's
// outcome is kept whenever the operation is already terminal, is unknown to us,
// or when the row carries no operation at all. A caller-supplied failure is
// always kept -- a handler that knows it failed knows more than the status.
const auditInsertSQL = `
	INSERT INTO audit_events
	  (actor_id, project_id, environment_id, operation_id, action, resource_kind, resource_name, outcome, metadata, actor_type)
	SELECT $1, $2, $3, $4, $5, $6, $7,
	       CASE
	         WHEN $8::text = 'success'
	          AND $4::uuid IS NOT NULL
	          AND EXISTS (
	               SELECT 1 FROM operations o
	                WHERE o.id = $4::uuid
	                  AND o.status NOT IN ('Committed', 'Ready', 'Failed')
	              )
	         THEN 'pending'
	         ELSE $8::text
	       END,
	       $9, $10`

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
// together with the reason ("cold", "idle", "relogin") a new visit was opened.
// The reason answers ONLY "why did this pod's memory decide to start a new
// visit" — "cold" means this pod has no memory of the user at all, which is
// true on every pod restart and every second replica, not just a user's true
// first-ever visit. Whether the user has ever been seen before, anywhere, is a
// question process memory cannot answer; the caller resolves that separately
// against the database before writing the audit row.
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
		return true, "cold"
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
//
// A nil actor is dropped on purpose. It means the caller could not name who did
// this, and a row that claims an action happened without saying who did it is
// worse than no row -- it pollutes every per-user count downstream.
func (h *Handler) recordAudit(ctx context.Context, actorID uuid.UUID, e auditEntry) {
	if actorID == uuid.Nil {
		return
	}
	h.writeAudit(ctx, actorID, e)
}

// recordSystemAudit writes a row for work the platform did on its own -- the
// autoscaler, the box loops, deploy hooks -- under the seeded "system" user.
//
// It exists because that user's id IS the zero uuid (migration 010_system_user.sql), which
// recordAudit reads as "no actor" and drops. The two cases are opposites: a nil
// actor there means the caller does not know who acted, while here the platform
// itself acted and is named. Routing system work through recordAudit would
// silently discard every row it wrote.
func (h *Handler) recordSystemAudit(ctx context.Context, e auditEntry) {
	h.writeAudit(ctx, systemDeployActorID, e)
}

func (h *Handler) writeAudit(ctx context.Context, actorID uuid.UUID, e auditEntry) {
	if h.pool == nil {
		return
	}
	writeAuditRow(ctx, h.pool, actorID, e)
}

// writeAuditRow is writeAudit's pool-only twin, for the background sweepers
// that have no Handler (SweepPlanExpiry, SweepQuotaGrace, and the payment-
// plan-mismatch detector all run off a bare *pgxpool.Pool). Same best-effort
// contract: a write failure is never surfaced to the caller.
func writeAuditRow(ctx context.Context, pool *pgxpool.Pool, actorID uuid.UUID, e auditEntry) {
	if pool == nil || e.Action == "" {
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
	if claim, ok := clientClaimFromContext(ctx); ok {
		meta = withClientClaimMetadata(meta, claim)
	}
	projectID, environmentID, operationID := e.ProjectID, e.EnvironmentID, e.OperationID
	unresolved := map[string]string{}

	actorType := auditActorTypeUser
	if actorID == systemDeployActorID {
		actorType = auditActorTypeSystem
	}

	for attempt := 0; attempt < 4; attempt++ {
		payload := meta
		if len(unresolved) > 0 {
			payload = mergeAuditMetadata(meta, unresolved)
		}
		_, err := pool.Exec(ctx, auditInsertSQL,
			actorID, nullableUUID(projectID), nullableUUID(environmentID), nullableUUID(operationID),
			e.Action, e.ResourceKind, e.ResourceName, outcome, payload, actorType,
		)
		if err == nil {
			return
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != pgForeignKeyViolation {
			logAuditWriteFailure(e.Action, "other", err)
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
			logAuditWriteFailure(e.Action, "fk_unresolved", err)
			return
		}
	}
	logAuditWriteFailure(e.Action, "fk_unresolved", errors.New("exhausted foreign key resolution attempts"))
}

// logAuditWriteFailure is the one place a dropped audit_events row becomes
// visible. writeAuditRow's contract is best-effort -- a failed audit write
// must never fail the request that triggered it -- but best-effort must not
// mean silent: before this, a Postgres error here (a constraint no caller
// anticipated, a column too narrow, a connection reset) vanished with no log
// line and no metric, and the row it would have written was gone for good.
// See backend/internal/metrics/audit_write.go for the live incident that
// exposed this.
func logAuditWriteFailure(action, reason string, err error) {
	log.Printf("audit: dropped %s row (%s): %v", action, reason, err)
	metrics.RecordAuditWriteFailure(action, reason)
}

// pgForeignKeyViolation is PostgreSQL's SQLSTATE for a violated foreign key.
const pgForeignKeyViolation = "23503"

// mergeAuditMetadata folds the unresolved-reference notes into an already
// marshalled metadata object, so the id that could not be stored in its column
// is still readable on the row.
func mergeAuditMetadata(meta []byte, extra map[string]string) []byte {
	merged := map[string]any{}
	if err := json.Unmarshal(meta, &merged); err != nil || merged == nil {
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

// auditWriteTimeout bounds a detached audit write. It is generous relative to a
// single INSERT and deliberately shorter than any caller's own budget.
const auditWriteTimeout = 5 * time.Second

// recordAuditDetached writes the row synchronously but OUT FROM UNDER the
// caller's cancellation.
//
// A failure row is written on the path where the request is most likely to be
// gone already: a caller that waited out its own timeout, or a client that hung
// up, cancels c.Request.Context() before the handler reaches the audit call, and
// pgx refuses to execute on a cancelled context. The row is not delayed, it is
// never written — so the one outcome the trail exists to explain is the one
// outcome missing from it. Live evidence: a box that recorded its error_message
// (written through context.Background()) while audit_events held no row at all
// for the same failure.
//
// This is not recordAuditAsync: the values on the caller's context (request id,
// tracing) are kept, and the write still completes before the handler returns,
// so a test can observe it without racing a goroutine.
func (h *Handler) recordAuditDetached(ctx context.Context, actorID uuid.UUID, e auditEntry) {
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), auditWriteTimeout)
	defer cancel()
	h.recordAudit(detached, actorID, e)
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

// auditVisitFirst/Return/Unknown are the only values ever written to the
// "visit" metadata key. "unknown" exists because a database miss must degrade
// to an honest "can't tell" rather than a guessed "first" — guessing would
// have reproduced the exact bug this classification replaces.
const (
	auditVisitFirst   = "first"
	auditVisitReturn  = "return"
	auditVisitUnknown = "unknown"
)

// classifyFirstVisit answers "has actorID ever appeared in audit_events
// before now", which per-pod memory cannot answer but a single indexed row
// lookup can. It runs ONLY from the async SessionStart path, and ONLY when
// observe() already decided the request opens a new visit — every other
// authenticated request skips this query entirely. A query failure must not
// invent an answer, so it reports "unknown" rather than falling back to
// "first" or "return".
//
// SignUp is excluded from the lookup: provisioning writes that row in the same
// statement that creates the users row (backend/internal/auth/provision.go), so
// counting it as prior history would make every genuinely first visit report
// "return".
func (h *Handler) classifyFirstVisit(ctx context.Context, actorID uuid.UUID, now time.Time) string {
	if h.pool == nil {
		return auditVisitUnknown
	}
	var exists bool
	err := h.pool.QueryRow(ctx,
		`SELECT EXISTS(
		     SELECT 1 FROM audit_events
		      WHERE actor_id = $1 AND created_at < $2 AND action <> 'SignUp'
		      LIMIT 1)`,
		actorID, now,
	).Scan(&exists)
	if err != nil {
		return auditVisitUnknown
	}
	if exists {
		return auditVisitReturn
	}
	return auditVisitFirst
}

// claimVisit is the cross-replica half of the visit boundary. auditVisits is
// per-pod memory, and a browser opening a page fires several requests at once
// that land on DIFFERENT replicas — each of which then legitimately believes it
// is seeing a new visit. Live count before this gate: 49 SessionStart rows for
// 38 distinct (actor, second) pairs, a 22% overstatement of the number every
// return-rate and activation figure is divided by.
//
// The claim is keyed by the Keycloak session id, not by time, so the two cases
// that must stay distinguishable stay distinguishable: a re-login within
// seconds brings a new sid and opens a new visit, while a second replica
// handling the same page load carries the same sid and is dropped. The TTL
// matches the idle gap, so a genuine return after that gap — which by
// definition happens at least that long after the visit began — finds the claim
// expired.
//
// Fail-open like the rest of the cache package: no Redis means every replica
// records, which is the behaviour this replaces, not a worse one.
func (h *Handler) claimVisit(ctx context.Context, actorID uuid.UUID, sid string) bool {
	key := "audit:visit:" + actorID.String()
	if sid != "" {
		key += ":" + sid
	}
	return h.cache.TryClaim(ctx, key, auditSessionIdleGap)
}

// writeSessionStart is the synchronous body of recordSessionStartAsync,
// pulled out so tests can drive it without racing a goroutine.
func (h *Handler) writeSessionStart(ctx context.Context, actorID uuid.UUID, username, path, reason, sid string) {
	if !h.claimVisit(ctx, actorID, sid) {
		return
	}
	visit := h.classifyFirstVisit(ctx, actorID, time.Now())
	h.recordAudit(ctx, actorID, auditEntry{
		Action:       auditActionSessionStart,
		ResourceKind: "Session",
		ResourceName: username,
		Metadata:     map[string]any{"path": path, "visit": visit, "reason": reason},
	})
}

// recordSessionStartAsync writes the SessionStart row off the request's hot
// path, same as recordAuditAsync, plus the one extra database round trip
// classifyFirstVisit needs. That round trip happens after the response has
// already been sent, so it never touches the request's latency budget; it
// only ever runs once per new visit, never once per request.
func (h *Handler) recordSessionStartAsync(actorID uuid.UUID, username, path, reason, sid string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.writeSessionStart(ctx, actorID, username, path, reason, sid)
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
				h.recordSessionStartAsync(claims.UserID, claims.Username, c.FullPath(), reason, claims.SessionID)
			}
		}
		c.Next()
	}
}

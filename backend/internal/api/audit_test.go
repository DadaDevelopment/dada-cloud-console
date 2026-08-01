package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/dada-tuda/console/backend/internal/cache"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAuditSeenCollapsesWithinWindow(t *testing.T) {
	s := newAuditSeen(30 * time.Minute)
	now := time.Now()

	if !s.allow("user-a", now) {
		t.Fatal("first occurrence must be recorded")
	}
	if s.allow("user-a", now.Add(29*time.Minute)) {
		t.Fatal("second occurrence inside the window must be collapsed")
	}
	if !s.allow("user-a", now.Add(31*time.Minute)) {
		t.Fatal("occurrence past the window must be recorded again")
	}
	if !s.allow("user-b", now) {
		t.Fatal("a different key must not be collapsed by another key's window")
	}
}

func TestAuditSeenResetsOnOverflow(t *testing.T) {
	s := newAuditSeen(time.Hour)
	now := time.Now()
	for i := 0; i < auditSeenLimit; i++ {
		s.allow(uuid.New().String(), now)
	}
	if len(s.seen) != auditSeenLimit {
		t.Fatalf("expected tracker filled to the limit, got %d", len(s.seen))
	}
	s.allow("overflow", now)
	if len(s.seen) != 1 {
		t.Fatalf("overflow must drop the map and keep only the newest key, got %d", len(s.seen))
	}
	if !s.allow("overflow-2", now) {
		t.Fatal("tracker must stay usable after an overflow reset")
	}
}

func TestNullableUUID(t *testing.T) {
	if nullableUUID(uuid.Nil) != nil {
		t.Fatal("uuid.Nil must be written as SQL NULL, not as the zero uuid")
	}
	id := uuid.New()
	if nullableUUID(id) != any(id) {
		t.Fatal("a real uuid must be passed through unchanged")
	}
}

func TestAuditVisitsKeepsOneRowPerVisit(t *testing.T) {
	v := newAuditVisits(30 * time.Minute)
	now := time.Now()

	newVisit, reason := v.observe("user-a", "sid-1", now)
	if !newVisit || reason != "cold" {
		t.Fatalf("first request must open a visit, got %v/%q", newVisit, reason)
	}
	if newVisit, _ = v.observe("user-a", "sid-1", now.Add(20*time.Minute)); newVisit {
		t.Fatal("continued activity inside the same visit must not open a new one")
	}
	if newVisit, _ = v.observe("user-a", "sid-1", now.Add(40*time.Minute)); newVisit {
		t.Fatal("gap is measured from the last request, not the last recorded row")
	}
	newVisit, reason = v.observe("user-a", "sid-1", now.Add(1*time.Hour+20*time.Minute))
	if !newVisit || reason != "idle" {
		t.Fatalf("a real idle gap must open a new visit, got %v/%q", newVisit, reason)
	}
}

func TestAuditVisitsSeparatesRelogin(t *testing.T) {
	v := newAuditVisits(30 * time.Minute)
	now := time.Now()

	if newVisit, _ := v.observe("user-a", "sid-1", now); !newVisit {
		t.Fatal("first request must open a visit")
	}
	newVisit, reason := v.observe("user-a", "sid-2", now.Add(6*time.Minute))
	if !newVisit || reason != "relogin" {
		t.Fatalf("a new keycloak session six minutes later is a second visit, got %v/%q", newVisit, reason)
	}
	if newVisit, _ := v.observe("user-a", "", now.Add(7*time.Minute)); newVisit {
		t.Fatal("a missing sid must not be read as a session change")
	}
	if newVisit, _ := v.observe("user-b", "sid-9", now.Add(6*time.Minute)); !newVisit {
		t.Fatal("another user's visit is independent")
	}
}

// TestClassifyFirstVisitUnknownWithoutPool covers the fail-open path: no
// database connection must yield "unknown", never a guessed "first".
func TestClassifyFirstVisitUnknownWithoutPool(t *testing.T) {
	h := &Handler{}
	got := h.classifyFirstVisit(context.Background(), uuid.New(), time.Now())
	if got != auditVisitUnknown {
		t.Fatalf("expected unknown without a pool, got %q", got)
	}
}

// TestClassifyFirstVisitUnknownOnQueryError covers a live query failure —
// here, a context already cancelled before the call — degrading to
// "unknown" rather than any guessed value.
func TestClassifyFirstVisitUnknownOnQueryError(t *testing.T) {
	pool := testAuditPool(t)
	h := &Handler{pool: pool}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := h.classifyFirstVisit(ctx, uuid.New(), time.Now())
	if got != auditVisitUnknown {
		t.Fatalf("expected unknown on query error, got %q", got)
	}
}

// TestWriteSessionStartCleanActorIsFirst is a brand-new actor with zero prior
// audit_events rows: the honest answer is "first", and reason travels through
// unchanged as the metadata's separate "why a new visit" key.
func TestWriteSessionStartCleanActorIsFirst(t *testing.T) {
	pool := testAuditPool(t)
	actorID, _ := seedAuditActor(t, pool)
	h := &Handler{pool: pool}
	ctx := context.Background()

	h.writeSessionStart(ctx, actorID, "someone", "/api/v1/onboarding", "cold", "")

	visit, reason := fetchSessionStartVisitReason(t, pool, actorID)
	if visit != auditVisitFirst {
		t.Fatalf("expected visit=first for a clean actor, got %q", visit)
	}
	if reason != "cold" {
		t.Fatalf("expected reason to pass through unchanged, got %q", reason)
	}
}

// TestWriteSessionStartReturningActorIsReturn is the bug this change fixes:
// a user with an older audit_events row who now opens a new visit purely
// because this pod's memory is cold must be recorded as a RETURN, not a new
// user — "cold" is the reason the visit is new, "return" is who the user is.
func TestWriteSessionStartReturningActorIsReturn(t *testing.T) {
	pool := testAuditPool(t)
	actorID, projectID := seedAuditActor(t, pool)
	ctx := context.Background()

	if _, err := pool.Exec(ctx,
		`INSERT INTO audit_events (actor_id, project_id, action, resource_kind, resource_name, outcome, metadata, created_at)
		 VALUES ($1, $2, 'ViewProject', 'Project', 'p', 'success', '{}', now() - interval '8 days')`,
		actorID, projectID,
	); err != nil {
		t.Fatalf("seed prior audit row: %v", err)
	}

	h := &Handler{pool: pool}
	h.writeSessionStart(ctx, actorID, "someone", "/api/v1/onboarding", "cold", "")

	visit, reason := fetchSessionStartVisitReason(t, pool, actorID)
	if visit != auditVisitReturn {
		t.Fatalf("a returning user with an 8-day-old row must be visit=return, got %q", visit)
	}
	if reason != "cold" {
		t.Fatalf("expected reason=cold to survive, got %q", reason)
	}
}

// TestWriteSessionStartCarriesIdleAndReloginReason checks that reason is not
// hard-coded to the first-visit case — it must reflect whatever observe()
// decided actually triggered the new visit.
func TestWriteSessionStartCarriesIdleAndReloginReason(t *testing.T) {
	pool := testAuditPool(t)
	h := &Handler{pool: pool}
	ctx := context.Background()

	for _, reason := range []string{"idle", "relogin"} {
		actorID, _ := seedAuditActor(t, pool)
		h.writeSessionStart(ctx, actorID, "someone", "/api/v1/onboarding", reason, "")

		_, gotReason := fetchSessionStartVisitReason(t, pool, actorID)
		if gotReason != reason {
			t.Fatalf("expected reason=%q to be recorded, got %q", reason, gotReason)
		}
	}
}

func fetchSessionStartVisitReason(t *testing.T, pool *pgxpool.Pool, actorID uuid.UUID) (string, string) {
	t.Helper()
	var metaRaw []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT metadata FROM audit_events WHERE actor_id = $1 AND action = $2 ORDER BY created_at DESC LIMIT 1`,
		actorID, auditActionSessionStart,
	).Scan(&metaRaw); err != nil {
		t.Fatalf("fetch SessionStart row: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	visit, _ := meta["visit"].(string)
	reason, _ := meta["reason"].(string)
	return visit, reason
}

func TestAuditVisitsResetsOnOverflow(t *testing.T) {
	v := newAuditVisits(time.Hour)
	now := time.Now()

	for i := 0; i < auditSeenLimit+1; i++ {
		v.observe(uuid.NewString(), "sid", now)
	}
	if len(v.users) > auditSeenLimit {
		t.Fatalf("tracker must not grow past the cap, got %d", len(v.users))
	}
}

// TestWriteSessionStartDedupesConcurrentReplicas is the +22% overstatement:
// one page load fires several requests, they land on different replicas, and
// each replica's own memory honestly reports a new visit. Sharing one Redis
// claim keyed by the Keycloak session id leaves exactly one row.
func TestWriteSessionStartDedupesConcurrentReplicas(t *testing.T) {
	pool := testAuditPool(t)
	actorID, _ := seedAuditActor(t, pool)
	ctx := context.Background()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	replicaA := &Handler{pool: pool, cache: cache.New(mr.Addr())}
	replicaB := &Handler{pool: pool, cache: cache.New(mr.Addr())}
	defer replicaA.cache.Close()
	defer replicaB.cache.Close()

	replicaA.writeSessionStart(ctx, actorID, "someone", "/api/v1/projects", "cold", "sid-1")
	replicaB.writeSessionStart(ctx, actorID, "someone", "/api/v1/apps", "cold", "sid-1")

	if got := countSessionStarts(t, pool, actorID); got != 1 {
		t.Fatalf("one page load across two replicas must leave one row, got %d", got)
	}

	// A re-login within the same window is a genuinely new visit and carries a
	// new sid, so it must NOT be swallowed by the claim of the previous one.
	replicaB.writeSessionStart(ctx, actorID, "someone", "/api/v1/projects", "relogin", "sid-2")
	if got := countSessionStarts(t, pool, actorID); got != 2 {
		t.Fatalf("a re-login must open a new visit, got %d rows", got)
	}
}

// TestWriteSessionStartRecordsWithoutRedis is the fail-open half: no cache
// configured must degrade to the previous behaviour (every replica records),
// never to silence.
func TestWriteSessionStartRecordsWithoutRedis(t *testing.T) {
	pool := testAuditPool(t)
	actorID, _ := seedAuditActor(t, pool)
	h := &Handler{pool: pool}

	h.writeSessionStart(context.Background(), actorID, "someone", "/api/v1/projects", "cold", "sid-1")

	if got := countSessionStarts(t, pool, actorID); got != 1 {
		t.Fatalf("a disabled cache must not gate the row, got %d", got)
	}
}

// TestRecordSystemAuditWritesRow guards the trap that ate the box worker's
// history: the platform's own actor id IS the zero uuid, which recordAudit
// treats as "nobody named an actor" and drops. Anything the platform does on
// its own must land, and it must land attributed to the system user.
func TestRecordSystemAuditWritesRow(t *testing.T) {
	pool := testAuditPool(t)
	_, projectID := seedAuditActor(t, pool)
	h := &Handler{pool: pool}

	h.recordSystemAudit(context.Background(), auditEntry{
		ProjectID:    projectID,
		Action:       "DeleteBox",
		ResourceKind: "Box",
		ResourceName: "probe",
		Outcome:      auditOutcomeSuccess,
	})

	var actor uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT actor_id FROM audit_events WHERE project_id = $1 AND action = 'DeleteBox'`,
		projectID,
	).Scan(&actor); err != nil {
		t.Fatalf("platform work must leave a row: %v", err)
	}
	if actor != systemDeployActorID {
		t.Fatalf("expected the system actor, got %s", actor)
	}
}

// TestRecordAuditDropsNilActor is the other half, and the reason the split
// exists: a caller that could not name who acted still writes nothing.
func TestRecordAuditDropsNilActor(t *testing.T) {
	pool := testAuditPool(t)
	_, projectID := seedAuditActor(t, pool)
	h := &Handler{pool: pool}

	h.recordAudit(context.Background(), uuid.Nil, auditEntry{
		ProjectID: projectID, Action: "DeleteBox", ResourceKind: "Box", ResourceName: "probe",
	})

	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events WHERE project_id = $1`, projectID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("an unnamed actor must not be recorded as the system, got %d rows", n)
	}
}

func countSessionStarts(t *testing.T, pool *pgxpool.Pool, actorID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events WHERE actor_id = $1 AND action = $2`,
		actorID, auditActionSessionStart,
	).Scan(&n); err != nil {
		t.Fatalf("count SessionStart rows: %v", err)
	}
	return n
}

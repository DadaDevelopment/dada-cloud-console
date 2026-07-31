package api

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/box"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fixedWorkerTestTime anchors every FakeClock this file constructs, so a
// worker test never depends on wall-clock time.
var fixedWorkerTestTime = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

// fakeDoor is a box.Door with no broker, matching a host that never set
// BOX_BROKER_DIR. bootBoxInstance treats that as a fallback, not an error, so
// tests that exercise ActionBoxUp through the worker do not need a real
// broker binary.
type fakeDoor struct{}

func (fakeDoor) BrokerConfigured() bool { return false }
func (fakeDoor) InstallSessionDigests(context.Context, *box.Instance, []box.SessionDigest) error {
	return nil
}
func (fakeDoor) RevokeAllSessionDigests(context.Context, *box.Instance) error { return nil }
func (fakeDoor) StartBroker(context.Context, *box.Instance, string) (string, error) {
	return "", box.ErrNoBroker
}

// newTestBoxWorkerHandler builds the minimal Handler the worker touches: pool
// and boxStack. Every other field stays zero, which is safe because the
// worker only ever reaches h.pool and h.boxStack.
func newTestBoxWorkerHandler(pool *pgxpool.Pool, rt box.BoxRuntime, warmPool box.WarmPool) *Handler {
	return &Handler{
		pool: pool,
		boxStack: &boxRuntimeStack{
			runtime:  rt,
			pool:     warmPool,
			door:     fakeDoor{},
			image:    "warm-polyglot-1",
			region:   "ru1",
			sessions: "http://127.0.0.1:0",
		},
	}
}

// seedBoxOperation inserts a box row and a matching Created operation,
// returning both ids. It follows seedMeteredBox's shape (box_meter_test.go)
// rather than reusing it, because the worker tests need control over the
// box's status and instance_ref that seedMeteredBox does not expose.
func seedBoxOperation(t *testing.T, pool *pgxpool.Pool, status models.BoxStatus, instanceRef string, action string, payload any) (boxID, opID uuid.UUID, projectID uuid.UUID, boxName string) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"box-worker-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, projectID) })

	boxName = "bw-" + suffix
	var envID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type)
		 VALUES ($1, $2, $3, 'dev') RETURNING id`,
		projectID, boxName, boxName+"-ns",
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}

	if err := pool.QueryRow(ctx,
		`INSERT INTO boxes (project_id, environment_id, name, image, profile, region, status, instance_ref)
		 VALUES ($1, $2, $3, 'warm-polyglot-1', 'box-small', 'ru1', $4, $5) RETURNING id`,
		projectID, envID, boxName, string(status), instanceRef,
	).Scan(&boxID); err != nil {
		t.Fatalf("seed box: %v", err)
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 VALUES ($1, $2, $3, $4, $5, $6, 'Created', $7) RETURNING id`,
		boxSystemActorID, projectID, envID, action, models.ResourceKindBox, boxName, payloadBytes,
	).Scan(&opID); err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	return boxID, opID, projectID, boxName
}

func operationStatus(t *testing.T, pool *pgxpool.Pool, opID uuid.UUID) (status, errMsg string, attempts int) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT status, COALESCE(error_message, ''), attempts FROM operations WHERE id = $1`, opID,
	).Scan(&status, &errMsg, &attempts); err != nil {
		t.Fatalf("read operation: %v", err)
	}
	return status, errMsg, attempts
}

func boxStatus(t *testing.T, pool *pgxpool.Pool, boxID uuid.UUID) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM boxes WHERE id = $1`, boxID,
	).Scan(&status); err != nil {
		t.Fatalf("read box: %v", err)
	}
	return status
}

// TestBoxOperationsWorker_DeleteBoxCallsDestroyAndTombstonesTheBox is the
// headline claim this worker exists to make true: a DeleteBox operation must
// actually destroy the runtime instance, not just sit in the queue. Before
// this worker existed, DELETE /boxes/{name} destroyed nothing at all.
func TestBoxOperationsWorker_DeleteBoxCallsDestroyAndTombstonesTheBox(t *testing.T) {
	pool := testOptimisticPool(t)
	rt := &box.FakeRuntime{Clock: box.NewFakeClock(fixedWorkerTestTime)}
	warmPool := box.NewMemoryPool()

	boxID, opID, _, _ := seedBoxOperation(t, pool, models.BoxStatusReady, "fc-worker-1",
		models.ActionDeleteBox, models.DeleteBoxPayload{Reason: "user"})
	if _, err := pool.Exec(context.Background(),
		`UPDATE operations SET payload = $2 WHERE id = $1`, opID,
		mustMarshal(t, models.DeleteBoxPayload{BoxID: boxID, Reason: "user"})); err != nil {
		t.Fatalf("fix up payload with the real box id: %v", err)
	}

	h := newTestBoxWorkerHandler(pool, rt, warmPool)
	w := &boxOperationsWorker{h: h}
	w.tick(context.Background())

	if n := rt.Destroys(); n != 1 {
		t.Errorf("runtime.Destroy called %d times, want exactly 1: a DeleteBox operation must destroy the instance", n)
	}
	if got := boxStatus(t, pool, boxID); got != string(models.BoxStatusDeleted) {
		t.Errorf("box status = %q after DeleteBox executed, want Deleted", got)
	}
	status, errMsg, attempts := operationStatus(t, pool, opID)
	if status != string(models.OperationStatusReady) {
		t.Errorf("operation status = %q, want Ready (terminal success); error_message=%q", status, errMsg)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1: one successful claim should cost exactly one attempt", attempts)
	}
}

// TestBoxOperationsWorker_DeleteBoxIsIdempotentOnAnAlreadyDeletedBox proves a
// retried or duplicate DeleteBox operation on a box that is already gone does
// not call Destroy a second time or error out.
func TestBoxOperationsWorker_DeleteBoxIsIdempotentOnAnAlreadyDeletedBox(t *testing.T) {
	pool := testOptimisticPool(t)
	rt := &box.FakeRuntime{Clock: box.NewFakeClock(fixedWorkerTestTime)}
	warmPool := box.NewMemoryPool()

	boxID, opID, _, _ := seedBoxOperation(t, pool, models.BoxStatusDeleted, "",
		models.ActionDeleteBox, models.DeleteBoxPayload{Reason: "user"})
	if _, err := pool.Exec(context.Background(),
		`UPDATE operations SET payload = $2 WHERE id = $1`, opID,
		mustMarshal(t, models.DeleteBoxPayload{BoxID: boxID, Reason: "user"})); err != nil {
		t.Fatalf("fix up payload with the real box id: %v", err)
	}

	h := newTestBoxWorkerHandler(pool, rt, warmPool)
	w := &boxOperationsWorker{h: h}
	w.tick(context.Background())

	if n := rt.Destroys(); n != 0 {
		t.Errorf("runtime.Destroy called %d times on an already-deleted box, want 0", n)
	}
	status, errMsg, _ := operationStatus(t, pool, opID)
	if status != string(models.OperationStatusReady) {
		t.Errorf("operation status = %q, want Ready: a delete on an already-deleted box is success, not failure; error_message=%q", status, errMsg)
	}
}

// TestBoxOperationsWorker_UnhandledActionFailsImmediately pins the
// requirement that an action this worker does not implement must go
// terminally Failed on the first attempt, never retried into a result that
// can never succeed, and never left to hang forever the way every box
// operation did before this worker existed.
func TestBoxOperationsWorker_UnhandledActionFailsImmediately(t *testing.T) {
	pool := testOptimisticPool(t)
	rt := &box.FakeRuntime{Clock: box.NewFakeClock(fixedWorkerTestTime)}
	warmPool := box.NewMemoryPool()

	_, opID, _, _ := seedBoxOperation(t, pool, models.BoxStatusReady, "fc-worker-2",
		models.ActionSuspendBox, models.SuspendBoxPayload{})

	h := newTestBoxWorkerHandler(pool, rt, warmPool)
	w := &boxOperationsWorker{h: h}
	w.tick(context.Background())

	status, errMsg, attempts := operationStatus(t, pool, opID)
	if status != string(models.OperationStatusFailed) {
		t.Errorf("operation status = %q, want Failed on the first attempt: an unimplemented action can never succeed on retry", status)
	}
	if errMsg == "" {
		t.Error("error_message empty on a failed operation: a caller polling it must be told why")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1: an unimplemented action must not spend the retry budget", attempts)
	}
}

// TestBoxOperationsWorker_ClaimNeverDoubleClaims is the SKIP LOCKED
// correctness test: with console running two replicas, both polling the same
// operations table on the same 5-second tick, the same row must never be
// claimed and executed twice.
func TestBoxOperationsWorker_ClaimNeverDoubleClaims(t *testing.T) {
	pool := testOptimisticPool(t)
	boxID, opID, _, _ := seedBoxOperation(t, pool, models.BoxStatusReady, "fc-worker-3",
		models.ActionDeleteBox, models.DeleteBoxPayload{Reason: "user"})
	if _, err := pool.Exec(context.Background(),
		`UPDATE operations SET payload = $2 WHERE id = $1`, opID,
		mustMarshal(t, models.DeleteBoxPayload{BoxID: boxID, Reason: "user"})); err != nil {
		t.Fatalf("fix up payload with the real box id: %v", err)
	}

	const replicas = 8
	workers := make([]*boxOperationsWorker, replicas)
	for i := range workers {
		workers[i] = &boxOperationsWorker{h: newTestBoxWorkerHandler(pool, &box.FakeRuntime{Clock: box.NewFakeClock(fixedWorkerTestTime)}, box.NewMemoryPool())}
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	claimed := make([]int, replicas)
	for i, w := range workers {
		wg.Add(1)
		go func(i int, w *boxOperationsWorker) {
			defer wg.Done()
			<-start
			ops, err := w.claim(context.Background())
			if err != nil {
				t.Errorf("replica %d: claim failed: %v", i, err)
				return
			}
			claimed[i] = len(ops)
		}(i, w)
	}
	close(start)
	wg.Wait()

	total := 0
	for _, n := range claimed {
		total += n
	}
	if total != 1 {
		t.Errorf("%d replicas together claimed %d rows for 1 seeded operation, want exactly 1: "+
			"FOR UPDATE SKIP LOCKED must make a claim exclusive across concurrent replicas", replicas, total)
	}
	status, _, attempts := operationStatus(t, pool, opID)
	if status != string(models.OperationStatusReconciling) {
		t.Errorf("operation status = %q after claim, want Reconciling", status)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d after %d concurrent claim attempts, want 1: only the winning claim may increment it", attempts, replicas)
	}
}

// TestBoxOperationsWorker_BoxUpBringsTheBoxReady proves the async CreateBox
// door's operation, once claimed, produces exactly what the synchronous BoxUp
// door produces: a Ready box with the runtime's instance coordinates —
// through bootBoxInstance, the same call both doors make.
func TestBoxOperationsWorker_BoxUpBringsTheBoxReady(t *testing.T) {
	pool := testOptimisticPool(t)
	deps, spec, _, _, _ := box.NewWarmFixture(0)

	boxID, opID, _, boxName := seedBoxOperation(t, pool, models.BoxStatusRequested, "",
		models.ActionBoxUp, models.BoxUpPayload{})
	if _, err := pool.Exec(context.Background(),
		`UPDATE operations SET payload = $2 WHERE id = $1`, opID,
		mustMarshal(t, models.BoxUpPayload{BoxID: boxID, Name: boxName, Image: spec.Image, Profile: spec.Profile, Region: spec.Region})); err != nil {
		t.Fatalf("fix up payload with the real box id: %v", err)
	}

	h := newTestBoxWorkerHandler(pool, deps.Runtime, deps.Pool)
	w := &boxOperationsWorker{h: h}
	w.tick(context.Background())

	if got := boxStatus(t, pool, boxID); got != string(models.BoxStatusReady) {
		t.Errorf("box status = %q after BoxUp executed, want Ready", got)
	}
	status, errMsg, _ := operationStatus(t, pool, opID)
	if status != string(models.OperationStatusReady) {
		t.Errorf("operation status = %q, want Ready; error_message=%q", status, errMsg)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

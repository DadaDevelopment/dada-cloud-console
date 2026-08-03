package api

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/box"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// vanishedDoor is a configured broker whose box no longer has a body — the state
// a pod left behind by a node drain, an eviction or a teardown that already ran.
type vanishedDoor struct{ fakeDoor }

func (vanishedDoor) BrokerConfigured() bool { return true }
func (vanishedDoor) RevokeAllSessionDigests(context.Context, *box.Instance) error {
	return fmt.Errorf("exec in box: %w: pods \"box-gone\" not found", box.ErrBodyGone)
}

// boxExpiry reads a box's sleep deadline.
func boxExpiry(t *testing.T, pool *pgxpool.Pool, boxID uuid.UUID) time.Time {
	t.Helper()
	var expires *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT expires_at FROM boxes WHERE id = $1`, boxID).Scan(&expires); err != nil {
		t.Fatalf("read expires_at: %v", err)
	}
	if expires == nil {
		return time.Time{}
	}
	return *expires
}

// TestBoxOperationsWorker_SuspendSleepsTheBoxWithoutDestroyingIt is the money
// test for sleep: the reaper enqueues a SuspendBox for every box past its idle
// timeout or TTL, and while nothing consumed those operations an abandoned box
// held a pod and a 20Gi volume forever — six of them and the fleet quota is
// full and nobody can create a box at all.
//
// Destroys() is asserted to be zero because the mistake this guards against is
// implementing sleep as a delete: that frees the quota too, and silently throws
// away the customer's work.
func TestBoxOperationsWorker_SuspendSleepsTheBoxWithoutDestroyingIt(t *testing.T) {
	pool := testOptimisticPool(t)
	rt := &box.FakeRuntime{Clock: box.NewFakeClock(fixedWorkerTestTime)}

	boxID, opID, _, _ := seedBoxOperation(t, pool, models.BoxStatusReady, "fc-sleep-1",
		models.ActionSuspendBox, models.SuspendBoxPayload{})
	if _, err := pool.Exec(context.Background(),
		`UPDATE operations SET payload = $2 WHERE id = $1`, opID,
		mustMarshal(t, models.SuspendBoxPayload{BoxID: boxID, Reason: "idle"})); err != nil {
		t.Fatalf("fix up payload with the real box id: %v", err)
	}

	w := &boxOperationsWorker{h: newTestBoxWorkerHandler(pool, rt, box.NewMemoryPool())}
	w.tick(context.Background())

	if n := rt.Suspends(); n != 1 {
		t.Errorf("runtime.Suspend called %d times, want exactly 1", n)
	}
	if n := rt.Destroys(); n != 0 {
		t.Errorf("runtime.Destroy called %d times during a suspend, want 0: sleep keeps the workspace disk", n)
	}
	if got := boxStatus(t, pool, boxID); got != string(models.BoxStatusSleeping) {
		t.Errorf("box status = %q after SuspendBox executed, want Sleeping", got)
	}
	status, errMsg, _ := operationStatus(t, pool, opID)
	if status != string(models.OperationStatusReady) {
		t.Errorf("operation status = %q, want Ready; error_message=%q", status, errMsg)
	}
}

// TestBoxOperationsWorker_SuspendIsIdempotentOnASleepingBox: the reaper can
// enqueue a suspend for a box that another replica already put down, and the
// spend cap can enqueue one on top of that. Neither is a failure.
func TestBoxOperationsWorker_SuspendIsIdempotentOnASleepingBox(t *testing.T) {
	pool := testOptimisticPool(t)
	rt := &box.FakeRuntime{Clock: box.NewFakeClock(fixedWorkerTestTime)}

	boxID, opID, _, _ := seedBoxOperation(t, pool, models.BoxStatusSleeping, "fc-sleep-2",
		models.ActionSuspendBox, models.SuspendBoxPayload{})
	if _, err := pool.Exec(context.Background(),
		`UPDATE operations SET payload = $2 WHERE id = $1`, opID,
		mustMarshal(t, models.SuspendBoxPayload{BoxID: boxID, Reason: "ttl"})); err != nil {
		t.Fatalf("fix up payload with the real box id: %v", err)
	}

	w := &boxOperationsWorker{h: newTestBoxWorkerHandler(pool, rt, box.NewMemoryPool())}
	w.tick(context.Background())

	if n := rt.Suspends(); n != 0 {
		t.Errorf("runtime.Suspend called %d times on an already sleeping box, want 0", n)
	}
	status, errMsg, _ := operationStatus(t, pool, opID)
	if status != string(models.OperationStatusReady) {
		t.Errorf("operation status = %q, want Ready: suspending a sleeping box is success; error_message=%q", status, errMsg)
	}
}

// TestBoxOperationsWorker_SuspendSucceedsWhenTheBodyIsAlreadyGone is a live
// regression. The first prod run of this worker failed every reaper-issued
// suspend with `revoke sessions: install session digests: exec in box: pods
// "box-…" not found`: the pods had already been deleted, revocation treated the
// missing door as an incomplete revocation, and each box stayed Ready while the
// reaper re-enqueued the same doomed suspend on every pass.
//
// A door that no longer exists cannot be knocked on. Suspend must land.
func TestBoxOperationsWorker_SuspendSucceedsWhenTheBodyIsAlreadyGone(t *testing.T) {
	pool := testOptimisticPool(t)
	rt := &box.FakeRuntime{Clock: box.NewFakeClock(fixedWorkerTestTime)}

	boxID, opID, _, _ := seedBoxOperation(t, pool, models.BoxStatusReady, "box-gone",
		models.ActionSuspendBox, models.SuspendBoxPayload{})
	if _, err := pool.Exec(context.Background(),
		`UPDATE operations SET payload = $2 WHERE id = $1`, opID,
		mustMarshal(t, models.SuspendBoxPayload{BoxID: boxID, Reason: "idle"})); err != nil {
		t.Fatalf("fix up payload with the real box id: %v", err)
	}

	h := newTestBoxWorkerHandler(pool, rt, box.NewMemoryPool())
	h.boxStack.door = vanishedDoor{}
	w := &boxOperationsWorker{h: h}
	w.tick(context.Background())

	status, errMsg, _ := operationStatus(t, pool, opID)
	if status != string(models.OperationStatusReady) {
		t.Errorf("operation status = %q, want Ready: a box with no body is already revoked; error_message=%q", status, errMsg)
	}
	if got := boxStatus(t, pool, boxID); got != string(models.BoxStatusSleeping) {
		t.Errorf("box status = %q, want Sleeping: a box left Ready with no pod is re-reaped forever", got)
	}
}

// TestBoxOperationsWorker_ResumeWakesTheBoxAndMovesItsDeadline pins the two
// things a wake must do beyond calling the runtime.
//
// The deadline is the subtle one. A box is put to sleep BECAUSE it reached
// expires_at, so a resume that leaves that timestamp alone hands back a box the
// very next reaper pass immediately puts to sleep again — a wake the customer
// watches succeed and then undo itself. The coordinates matter for the same
// kind of reason: a resumed box lives in a new pod with a new address, so a row
// still carrying the old one points at nothing.
func TestBoxOperationsWorker_ResumeWakesTheBoxAndMovesItsDeadline(t *testing.T) {
	pool := testOptimisticPool(t)
	rt := &box.FakeRuntime{Clock: box.NewFakeClock(fixedWorkerTestTime)}

	boxID, opID, _, _ := seedBoxOperation(t, pool, models.BoxStatusSleeping, "fc-sleep-3",
		models.ActionResumeBox, models.ResumeBoxPayload{})
	if _, err := pool.Exec(context.Background(),
		`UPDATE operations SET payload = $2 WHERE id = $1`, opID,
		mustMarshal(t, models.ResumeBoxPayload{BoxID: boxID})); err != nil {
		t.Fatalf("fix up payload with the real box id: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET expires_at = now() - INTERVAL '1 hour' WHERE id = $1`, boxID); err != nil {
		t.Fatalf("expire the box: %v", err)
	}

	w := &boxOperationsWorker{h: newTestBoxWorkerHandler(pool, rt, box.NewMemoryPool())}
	w.tick(context.Background())

	status, errMsg, _ := operationStatus(t, pool, opID)
	if status != string(models.OperationStatusReady) {
		t.Fatalf("operation status = %q, want Ready; error_message=%q", status, errMsg)
	}
	if n := rt.Resumes(); n != 1 {
		t.Errorf("runtime.Resume called %d times, want exactly 1", n)
	}
	if got := boxStatus(t, pool, boxID); got != string(models.BoxStatusReady) {
		t.Errorf("box status = %q after ResumeBox executed, want Ready", got)
	}
	if exp := boxExpiry(t, pool, boxID); !exp.After(time.Now()) {
		t.Errorf("expires_at = %s after a resume, want a deadline in the future: "+
			"a woken box that is still expired is put straight back to sleep", exp)
	}

	var sshHost string
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(ssh_host, '') FROM boxes WHERE id = $1`, boxID).Scan(&sshHost); err != nil {
		t.Fatalf("read ssh_host: %v", err)
	}
	if sshHost != "10.244.0.99" {
		t.Errorf("ssh_host = %q after a resume, want the address the rebuilt body reported", sshHost)
	}
}

// TestBoxOperationsWorker_ResumeRebindsIdentityBeforeCallingTheBoxReady.
// What survives a sleep is the workspace volume — not the environment file, not
// the authorized key, not the pod's labels, because those live in a container
// that no longer exists. A resume that skips the rebind returns a box the agent
// can reach and cannot use, and the canary is what proves it can.
func TestBoxOperationsWorker_ResumeRebindsIdentityBeforeCallingTheBoxReady(t *testing.T) {
	pool := testOptimisticPool(t)
	rt := &box.FakeRuntime{Clock: box.NewFakeClock(fixedWorkerTestTime)}

	boxID, opID, _, _ := seedBoxOperation(t, pool, models.BoxStatusSleeping, "fc-sleep-4",
		models.ActionResumeBox, models.ResumeBoxPayload{})
	if _, err := pool.Exec(context.Background(),
		`UPDATE operations SET payload = $2 WHERE id = $1`, opID,
		mustMarshal(t, models.ResumeBoxPayload{BoxID: boxID, SSHPublicKey: "ssh-ed25519 AAAAtest woken"})); err != nil {
		t.Fatalf("fix up payload with the real box id: %v", err)
	}

	w := &boxOperationsWorker{h: newTestBoxWorkerHandler(pool, rt, box.NewMemoryPool())}
	w.tick(context.Background())

	if got := len(rt.ExecutedCommands()); got == 0 {
		t.Error("a resume ran no command inside the box: ready must be proven by the canary, not assumed")
	}
}

// TestBoxOperationsWorker_ResumeThatNeverGoesReadyFailsRatherThanLies: a box
// whose rebuilt body comes back without its toolchain must not be reported
// Ready. The customer discovering it broken is strictly worse than being told.
func TestBoxOperationsWorker_ResumeThatNeverGoesReadyFailsRatherThanLies(t *testing.T) {
	pool := testOptimisticPool(t)
	missing := box.CanaryMissing("psql")
	rt := &box.FakeRuntime{Clock: box.NewFakeClock(fixedWorkerTestTime), Canary: &missing}

	boxID, opID, _, _ := seedBoxOperation(t, pool, models.BoxStatusSleeping, "fc-sleep-5",
		models.ActionResumeBox, models.ResumeBoxPayload{})
	if _, err := pool.Exec(context.Background(),
		`UPDATE operations SET payload = $2 WHERE id = $1`, opID,
		mustMarshal(t, models.ResumeBoxPayload{BoxID: boxID})); err != nil {
		t.Fatalf("fix up payload with the real box id: %v", err)
	}

	w := &boxOperationsWorker{h: newTestBoxWorkerHandler(pool, rt, box.NewMemoryPool())}
	w.tick(context.Background())

	if got := boxStatus(t, pool, boxID); got == string(models.BoxStatusReady) {
		t.Error("box status = Ready after a resume whose canary failed: a woken box without its toolchain is not ready")
	}
	status, _, _ := operationStatus(t, pool, opID)
	if status == string(models.OperationStatusReady) {
		t.Error("operation status = Ready after a failed canary: the wake did not succeed")
	}
}

// TestBoxOperationsWorker_SuspendStartsTheSleepClock is the fix for the most
// expensive defect this feature has had.
//
// reapSleeping only considers boxes whose slept_at is set, and for the whole life
// of the feature this UPDATE wrote the status without the stamp. Every box that
// went to sleep through an operation — which is every box the reaper itself puts
// down, at the idle timeout and at the TTL — landed in a state no sweep could ever
// see again, holding its workspace volume and metering suspended_disk forever. On
// 2026-08-04 that fleet was 15.6% of the platform bill with no external demand and
// 96% of all box minutes ever metered were the disks of boxes nobody could reap.
//
// The status and the stamp are one fact. Writing one without the other is what
// made the leak silent.
func TestBoxOperationsWorker_SuspendStartsTheSleepClock(t *testing.T) {
	pool := testOptimisticPool(t)
	rt := &box.FakeRuntime{Clock: box.NewFakeClock(fixedWorkerTestTime)}

	boxID, opID, _, _ := seedBoxOperation(t, pool, models.BoxStatusReady, "fc-sleep-clock",
		models.ActionSuspendBox, models.SuspendBoxPayload{})
	if _, err := pool.Exec(context.Background(),
		`UPDATE operations SET payload = $2 WHERE id = $1`, opID,
		mustMarshal(t, models.SuspendBoxPayload{BoxID: boxID, Reason: "idle"})); err != nil {
		t.Fatalf("fix up payload with the real box id: %v", err)
	}

	w := &boxOperationsWorker{h: newTestBoxWorkerHandler(pool, rt, box.NewMemoryPool())}
	w.tick(context.Background())

	var sleptAt *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT slept_at FROM boxes WHERE id = $1`, boxID).Scan(&sleptAt); err != nil {
		t.Fatalf("read slept_at: %v", err)
	}
	if sleptAt == nil {
		t.Fatal("slept_at is NULL after a suspend landed: the reaper filters on it, " +
			"so this box would hold its volume and bill suspended_disk until somebody found it by hand")
	}
	if since := time.Since(*sleptAt); since > time.Hour || since < -time.Hour {
		t.Errorf("slept_at is %s away from now; the stamp is the moment the box went down", since)
	}
}

// TestBoxOperationsWorker_ResumeClearsTheSleepAndWarningStamps covers the other
// half of the same fact.
//
// The three columns are the sleep episode. A box that has been woken is not asleep,
// and the warnings it received during that episode were about a deletion that no
// longer applies. Leaving them set means the NEXT sleep starts with both warnings
// already spent — the box would be destroyed on the first sweep past 72h with the
// customer never told, which is precisely the outcome the two-warning rule exists
// to prevent.
func TestBoxOperationsWorker_ResumeClearsTheSleepAndWarningStamps(t *testing.T) {
	pool := testOptimisticPool(t)
	rt := &box.FakeRuntime{Clock: box.NewFakeClock(fixedWorkerTestTime)}

	boxID, opID, _, _ := seedBoxOperation(t, pool, models.BoxStatusSleeping, "fc-sleep-wake",
		models.ActionResumeBox, models.ResumeBoxPayload{})
	if _, err := pool.Exec(context.Background(),
		`UPDATE operations SET payload = $2 WHERE id = $1`, opID,
		mustMarshal(t, models.ResumeBoxPayload{BoxID: boxID})); err != nil {
		t.Fatalf("fix up payload with the real box id: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes
		    SET slept_at             = now() - INTERVAL '67 hours',
		        reap_warned_at       = now() - INTERVAL '19 hours',
		        reap_final_warned_at = now() - INTERVAL '1 hour'
		  WHERE id = $1`, boxID); err != nil {
		t.Fatalf("seed a box that was one hour from deletion: %v", err)
	}

	w := &boxOperationsWorker{h: newTestBoxWorkerHandler(pool, rt, box.NewMemoryPool())}
	w.tick(context.Background())

	var sleptAt, warned, finalWarned *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT slept_at, reap_warned_at, reap_final_warned_at FROM boxes WHERE id = $1`, boxID,
	).Scan(&sleptAt, &warned, &finalWarned); err != nil {
		t.Fatalf("read the sleep stamps: %v", err)
	}
	if sleptAt != nil {
		t.Errorf("slept_at = %s on a woken box, want NULL", sleptAt)
	}
	if warned != nil || finalWarned != nil {
		t.Errorf("warning stamps survived the wake (warned=%v final=%v): the box's next sleep "+
			"would begin with both warnings spent and be deleted with nobody told", warned, finalWarned)
	}
}

package api

import (
	"context"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Dada Box reaper tests.
//
// All DB-backed, because every claim here is about rows and enqueued operations
// rather than about arithmetic. The clock is injected, so a box can be 71 hours
// asleep in a test that runs in 50 ms.

// newTestBoxReaper builds a reaper whose clock the caller controls, with no notifier
// (so nothing is mailed; the tests assert on the stamps and the operations, which is
// what actually changes the customer's world).
func newTestBoxReaper(t *testing.T, pool *pgxpool.Pool, clock *time.Time) *BoxReaper {
	t.Helper()
	return &BoxReaper{
		pool: pool,
		cfg:  boxMeterTestConfig(),
		now:  func() time.Time { return *clock },
	}
}

// TestBoxReaper_IdleBoxIsSuspendedNotDeleted: the idle sweep's job is to stop the
// meter, not to destroy anything. Fifteen minutes of nobody touching a box is a
// pause, not an abandonment.
func TestBoxReaper_IdleBoxIsSuspendedNotDeleted(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, boxID, boxName := seedMeteredBox(t, pool, "org-reap-"+uuid.NewString()[:8], models.BoxStatusReady)

	now := time.Now().UTC()
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET idle_timeout_seconds = 900, last_active_at = $2 WHERE id = $1`,
		boxID, now.Add(-20*time.Minute)); err != nil {
		t.Fatalf("age the box: %v", err)
	}

	clock := now
	r := newTestBoxReaper(t, pool, &clock)
	r.RunBoxMaintenanceTick(context.Background())

	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionSuspendBox); n != 1 {
		t.Errorf("%d suspends enqueued for a 20-minute-idle box, want exactly 1", n)
	}
	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionDeleteBox); n != 0 {
		t.Errorf("%d deletes enqueued for an idle box, want 0: idleness is a pause, not an abandonment", n)
	}
}

// TestBoxReaper_GuestHeartbeatDefersSuspension is the ONE thing an in-guest signal is
// allowed to do.
//
// The guest cannot reduce billing (box_meter_test.go asserts that from the other
// side), but it can ask to keep running — which means asking for MORE billing. An
// agent running a 40-minute build with nobody attached needs exactly this, and
// trusting it is safe precisely because the only thing it can do is cost its sender
// money.
func TestBoxReaper_GuestHeartbeatDefersSuspension(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, boxID, boxName := seedMeteredBox(t, pool, "org-reap-"+uuid.NewString()[:8], models.BoxStatusReady)

	now := time.Now().UTC()
	// Nothing has touched the box for 20 minutes — except the in-guest heartbeat,
	// one minute ago.
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET idle_timeout_seconds = 900, last_active_at = $2, guest_heartbeat_at = $3
		  WHERE id = $1`,
		boxID, now.Add(-20*time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatalf("age the box: %v", err)
	}

	clock := now
	r := newTestBoxReaper(t, pool, &clock)
	r.RunBoxMaintenanceTick(context.Background())

	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionSuspendBox); n != 0 {
		t.Errorf("%d suspends enqueued despite a fresh in-guest heartbeat, want 0: "+
			"an in-guest signal may always defer sleep, because deferring sleep asks for MORE billing", n)
	}
}

// TestBoxReaper_ExpiredTTLSleepsAndDoesNotDestroy pins the TTL semantics against the
// promise already published in swagger.json for ExtendBox: "Reaching the TTL puts a
// box to sleep, it never destroys it".
//
// This test exists specifically so a future change that makes the TTL destructive
// fails here, next to the sentence it would falsify, rather than in production next
// to a customer whose prototype is gone.
func TestBoxReaper_ExpiredTTLSleepsAndDoesNotDestroy(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, boxID, boxName := seedMeteredBox(t, pool, "org-reap-"+uuid.NewString()[:8], models.BoxStatusReady)

	now := time.Now().UTC()
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET expires_at = $2, last_active_at = $3 WHERE id = $1`,
		boxID, now.Add(-time.Minute), now); err != nil {
		t.Fatalf("expire the box: %v", err)
	}

	clock := now
	r := newTestBoxReaper(t, pool, &clock)
	r.RunBoxMaintenanceTick(context.Background())

	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionSuspendBox); n != 1 {
		t.Errorf("%d suspends enqueued for an expired box, want exactly 1", n)
	}
	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionDeleteBox); n != 0 {
		t.Errorf("%d deletes enqueued at the TTL, want 0 — the published contract for ExtendBox says "+
			"the TTL puts a box to sleep and never destroys it", n)
	}
}

// TestBoxReaper_SleepingBoxIsWarnedTwiceThenDeleted walks the 72-hour path in order.
//
// The ORDER is the assertion. A deletion is irreversible for everything that lives
// only inside the box, so the sequence — warn at 48h, warn again at 66h, delete at
// 72h — is the difference between a policy and a data loss incident.
func TestBoxReaper_SleepingBoxIsWarnedTwiceThenDeleted(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, boxID, boxName := seedMeteredBox(t, pool, "org-reap-"+uuid.NewString()[:8], models.BoxStatusSleeping)

	sleptAt := time.Now().UTC().Add(-100 * time.Hour)
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET slept_at = $2 WHERE id = $1`, boxID, sleptAt); err != nil {
		t.Fatalf("set slept_at: %v", err)
	}

	clock := sleptAt.Add(time.Hour)
	r := newTestBoxReaper(t, pool, &clock)

	readStamps := func() (warned, finalWarned *time.Time) {
		t.Helper()
		if err := pool.QueryRow(context.Background(),
			`SELECT reap_warned_at, reap_final_warned_at FROM boxes WHERE id = $1`, boxID,
		).Scan(&warned, &finalWarned); err != nil {
			t.Fatalf("read reap stamps: %v", err)
		}
		return warned, finalWarned
	}

	// One hour asleep: nothing at all.
	r.RunBoxMaintenanceTick(context.Background())
	if w, f := readStamps(); w != nil || f != nil {
		t.Fatalf("warned after one hour asleep: warned=%v final=%v", w, f)
	}

	// 49 hours: the first warning, and only the first.
	clock = sleptAt.Add(49 * time.Hour)
	r.RunBoxMaintenanceTick(context.Background())
	w, f := readStamps()
	if w == nil {
		t.Fatal("no first warning at 49h asleep")
	}
	if f != nil {
		t.Error("the final warning fired at 49h; two identically-timed warnings are read as a duplicate and ignored")
	}
	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionDeleteBox); n != 0 {
		t.Fatalf("%d deletes enqueued at 49h asleep, want 0", n)
	}

	// 67 hours: the final warning. Still nothing destroyed.
	clock = sleptAt.Add(67 * time.Hour)
	r.RunBoxMaintenanceTick(context.Background())
	if _, f = readStamps(); f == nil {
		t.Fatal("no final warning at 67h asleep")
	}
	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionDeleteBox); n != 0 {
		t.Fatalf("%d deletes enqueued at 67h asleep, want 0", n)
	}

	// 73 hours: destroyed, once, and the row is marked Deleting so a concurrent read
	// never hands out a body that is being torn down.
	clock = sleptAt.Add(73 * time.Hour)
	r.RunBoxMaintenanceTick(context.Background())
	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionDeleteBox); n != 1 {
		t.Errorf("%d deletes enqueued at 73h asleep, want exactly 1", n)
	}
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM boxes WHERE id = $1`, boxID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != string(models.BoxStatusDeleting) {
		t.Errorf("status = %q after the reap, want Deleting", status)
	}

	// And a second sweep does not enqueue a second delete: the box is no longer
	// Sleeping, so it is out of the sweep's scope.
	clock = sleptAt.Add(74 * time.Hour)
	r.RunBoxMaintenanceTick(context.Background())
	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionDeleteBox); n != 1 {
		t.Errorf("%d deletes after a second sweep, want still 1", n)
	}
}

// TestBoxReaper_RefusesToDeleteWithoutBothWarnings is the safety catch on the one
// destructive path.
//
// The clock alone is not authority to destroy somebody's work. If the mail path was
// broken for three days, the honest outcome is a box that survives and a loud log
// line — not a silent deletion the customer discovers by finding their prototype
// gone. So a box that is past 72h with an unsent warning gets the warning, not the
// axe.
func TestBoxReaper_RefusesToDeleteWithoutBothWarnings(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, boxID, boxName := seedMeteredBox(t, pool, "org-reap-"+uuid.NewString()[:8], models.BoxStatusSleeping)

	sleptAt := time.Now().UTC().Add(-200 * time.Hour)
	// Already 80 hours past the reap deadline, and only the FIRST warning ever went
	// out (the second one's send failed, or mail was down when it was due).
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET slept_at = $2, reap_warned_at = $3, reap_final_warned_at = NULL WHERE id = $1`,
		boxID, sleptAt, sleptAt.Add(48*time.Hour)); err != nil {
		t.Fatalf("seed partial warnings: %v", err)
	}

	clock := sleptAt.Add(152 * time.Hour)
	r := newTestBoxReaper(t, pool, &clock)
	r.RunBoxMaintenanceTick(context.Background())

	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionDeleteBox); n != 0 {
		t.Errorf("%d deletes enqueued with the second warning never sent, want 0: "+
			"a clock is not authority to destroy work the customer was not told twice about", n)
	}
	var finalWarned *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT reap_final_warned_at FROM boxes WHERE id = $1`, boxID).Scan(&finalWarned); err != nil {
		t.Fatalf("read final warning: %v", err)
	}
	if finalWarned == nil {
		t.Error("the missing warning was not sent either; the box would sit forever with nobody told")
	}

	// Now that both warnings exist, the next sweep may act — the catch is a delay, not
	// a permanent reprieve.
	clock = clock.Add(time.Minute)
	r.RunBoxMaintenanceTick(context.Background())
	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionDeleteBox); n != 1 {
		t.Errorf("%d deletes after both warnings exist, want exactly 1", n)
	}
}

// TestBoxReaper_LeavesCrystallizingBoxesAlone: a promotion in flight must not have a
// suspend queued behind it. models.CanTransitionBoxStatus is the gate, and this test
// is what proves the reaper consults it rather than trusting its own WHERE clause.
func TestBoxReaper_LeavesCrystallizingBoxesAlone(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, boxID, boxName := seedMeteredBox(t, pool, "org-reap-"+uuid.NewString()[:8], models.BoxStatusReady)

	now := time.Now().UTC()
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET idle_timeout_seconds = 900, last_active_at = $2 WHERE id = $1`,
		boxID, now.Add(-time.Hour)); err != nil {
		t.Fatalf("age the box: %v", err)
	}

	clock := now
	r := newTestBoxReaper(t, pool, &clock)
	// The candidate is loaded as Ready, then flips to Crystallizing before it is acted
	// on — the race the transition check exists for. Simulated by driving the check
	// directly, because the window itself is milliseconds wide.
	c := boxReapCandidate{
		BoxID: boxID, ProjectID: projectID, Name: boxName,
		Status: models.BoxStatusCrystallizing,
	}
	r.enqueueSuspend(context.Background(), c, "idle")
	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionSuspendBox); n != 0 {
		t.Errorf("%d suspends enqueued for a Crystallizing box, want 0: a promotion in flight must not "+
			"have a freeze queued behind it", n)
	}
}

// TestBoxReaperAdvisoryLockKeyIsDistinct guards the one thing a copy-pasted lock key
// would break invisibly: two loops sharing a key means one of them silently never
// runs on a replica that lost the race, and there is no error anywhere.
func TestBoxReaperAdvisoryLockKeyIsDistinct(t *testing.T) {
	keys := map[int64]string{
		lockKeyDomainReconcile: "domain-reconcile",
		lockKeyBackupReconcile: "backup-reconcile",
		lockKeyAppHealthWatch:  "app-health",
		lockKeyAppVolumeWatch:  "app-volume",
	}
	if name, clash := keys[lockKeyBoxReaper]; clash {
		t.Fatalf("lockKeyBoxReaper collides with %s: one of the two loops would silently never run", name)
	}
}

// TestBoxMeterTakesNoAdvisoryLock is the counterpart assertion, and it is a real one
// rather than a comment: the meter must remain runnable while the reaper's lock is
// held, because it is supposed to run on every replica.
//
// If somebody wrapped MeterBoxMinutes in runWithAdvisoryLock, a single replica's
// outage would turn into unbillable minutes that no backfill can recover — the
// signal is a sample stream, not a counter we can re-read.
func TestBoxMeterTakesNoAdvisoryLock(t *testing.T) {
	pool := testOptimisticPool(t)
	_, boxID, _ := seedMeteredBox(t, pool, "org-lock-"+uuid.NewString()[:8], models.BoxStatusReady)

	base := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	clock := base
	m := newTestBoxMeter(t, pool, boxMeterTestConfig(), &clock)

	// Hold the reaper's lock in one session while the meter runs in another. With a
	// lock the meter would write nothing.
	held := make(chan struct{})
	done := make(chan struct{})
	go func() {
		runWithAdvisoryLock(context.Background(), pool, lockKeyBoxReaper, "test-hold", func(context.Context) {
			close(held)
			<-done
		})
	}()
	<-held
	defer close(done)

	meterMinutes(t, pool, boxID, m, &clock, base, 3, func(int) bool { return true })
	if got := ledgerRows(t, pool, boxID)[boxUsageKindActive].Minutes; got != 3 {
		t.Errorf("the meter billed %d minutes while another session held the reaper lock, want 3: "+
			"the meter must not be lock-guarded", got)
	}
}

// TestBoxReaperConfigDefaults pins the four BOX_* knobs, because their defaults are
// product decisions rather than arbitrary numbers and a silent change to any of them
// changes what a customer is charged.
func TestBoxReaperConfigDefaults(t *testing.T) {
	// config.Load has two hard requirements unrelated to boxes; the four BOX_* knobs
	// are left unset, so what is asserted below is genuinely their defaults.
	t.Setenv("DB_URL", "postgres://unused@127.0.0.1:1/unused?sslmode=disable")
	t.Setenv("JWT_SECRET", "test-secret")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.BoxMeterIntervalSecs != 60 {
		t.Errorf("BOX_METER_INTERVAL_SECS default = %d, want 60: the ledger's grain is one minute, so a "+
			"longer period drops minutes nobody bills", cfg.BoxMeterIntervalSecs)
	}
	if cfg.BoxActiveWindowSecs != 120 {
		t.Errorf("BOX_ACTIVE_WINDOW_SECS default = %d, want 120 (two ticks, so one lost sample does not "+
			"bill an active box as idle)", cfg.BoxActiveWindowSecs)
	}
	if cfg.BoxActiveCPUPercent != 5 {
		t.Errorf("BOX_ACTIVE_CPU_PERCENT default = %v, want 5", cfg.BoxActiveCPUPercent)
	}
	if cfg.BoxDefaultSpendCapRub <= 0 {
		t.Errorf("BOX_DEFAULT_SPEND_CAP_RUB default = %v, want a positive cap: an unlimited default would "+
			"leave exactly the customers who did not think about caps unprotected", cfg.BoxDefaultSpendCapRub)
	}
}

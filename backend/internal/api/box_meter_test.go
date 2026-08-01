package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Dada Box metering tests.
//
// Structured in two layers on purpose, because the two questions are different:
//
//   - the ACTIVITY RULE is pure arithmetic over timestamps, so it is tested as
//     arithmetic. "10 active minutes out of 60 bills exactly 10" is a claim about a
//     rule, and a claim about a rule should not need a database to be provable.
//   - the LEDGER is a claim about the shape of stored rows — one row per minute,
//     NO row for an idle minute, the same row after a replay — and that can only be
//     tested against a real Postgres, behind the usual TEST_DATABASE_URL skip.
//
// Fixtures follow storage_cap_test.go: a throwaway org-owned project registered for
// cleanup, so a failed test cannot leave rows that make the next run pass or fail
// for the wrong reason.

// boxMeterTestConfig is the knob set the meter tests run under: the real defaults,
// plus a spend cap high enough that the cap logic never interferes with a test that
// is about minutes. The cap has its own tests.
func boxMeterTestConfig() *config.Config {
	return &config.Config{
		BillingEnabled:        true,
		BoxMeterIntervalSecs:  60,
		BoxActiveWindowSecs:   120,
		BoxActiveCPUPercent:   5,
		BoxDefaultSpendCapRub: 1e9,
	}
}

// --- layer 1: the activity rule, as arithmetic ---

// activeSample builds the signals of a box whose out-of-guest sample says "busy",
// taken at ts.
func activeSample(ts time.Time) boxActivitySignals {
	return boxActivitySignals{
		Status:            models.BoxStatusReady,
		AgentSampleAt:     &ts,
		AgentSampleActive: true,
	}
}

// idleSample builds the signals of a box whose out-of-guest sample says "idle",
// taken at ts. The sample is FRESH — that is the point: this is a box we are
// definitely observing, and the observation is "nothing is happening".
func idleSample(ts time.Time) boxActivitySignals {
	return boxActivitySignals{
		Status:            models.BoxStatusReady,
		AgentSampleAt:     &ts,
		AgentSampleActive: false,
	}
}

// TestClassifyBoxMinuteBillsTenOfSixty is the headline metering claim, at the level
// of the rule: an hour in which the box worked for ten minutes bills ten minutes,
// and the other fifty are not billed at all.
func TestClassifyBoxMinuteBillsTenOfSixty(t *testing.T) {
	base := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	const window = 120 * time.Second

	var active, idle int
	for i := 0; i < 60; i++ {
		minuteStart := base.Add(time.Duration(i) * time.Minute)
		minuteEnd := minuteStart.Add(time.Minute)
		// The sample for this minute lands in the middle of it.
		sampledAt := minuteStart.Add(30 * time.Second)
		signals := idleSample(sampledAt)
		if i < 10 {
			signals = activeSample(sampledAt)
		}
		switch classifyBoxMinute(signals, minuteEnd, window, 5) {
		case boxUsageKindActive:
			active++
		case "":
			idle++
		default:
			t.Fatalf("minute %d classified as an unexpected kind", i)
		}
	}
	if active != 10 {
		t.Errorf("billed %d active minutes, want exactly 10", active)
	}
	if idle != 50 {
		t.Errorf("counted %d idle minutes, want exactly 50", idle)
	}
}

// TestClassifyBoxMinuteSleepingBoxBillsNoActiveMinute: a sleeping box bills ZERO
// active minutes no matter what any signal says, and bills its disk instead.
//
// The second half of that sentence is what keeps the first half honest. "Idle is
// free" would be a lie if a 40 GiB rootfs sat on our storage for 72 hours at no
// charge — so the sleeping minute is billed, under a different kind, at a
// storage-only footprint.
func TestClassifyBoxMinuteSleepingBoxBillsNoActiveMinute(t *testing.T) {
	minuteEnd := time.Date(2026, 7, 30, 9, 1, 0, 0, time.UTC)
	sampledAt := minuteEnd.Add(-30 * time.Second)
	cpu := 99.0

	// Every activity signal at once, and the box still bills no active minute.
	s := boxActivitySignals{
		Status:            models.BoxStatusSleeping,
		AgentSampleAt:     &sampledAt,
		AgentSampleActive: true,
		AgentCPUPercent:   &cpu,
		GuestHeartbeatAt:  &sampledAt,
		TouchedAt:         &sampledAt,
		OperationInFlight: true,
	}
	if got := classifyBoxMinute(s, minuteEnd, 120*time.Second, 5); got != boxUsageKindSuspendedDisk {
		t.Fatalf("sleeping box classified as %q, want %q", got, boxUsageKindSuspendedDisk)
	}
}

// TestClassifyBoxMinuteTombstonesAndFailuresBillNothing: a box being torn down or one
// that never worked has nothing left to consume, and a Failed box never gave the
// customer anything to pay for.
func TestClassifyBoxMinuteTombstonesAndFailuresBillNothing(t *testing.T) {
	minuteEnd := time.Date(2026, 7, 30, 9, 1, 0, 0, time.UTC)
	sampledAt := minuteEnd.Add(-10 * time.Second)
	for _, status := range []models.BoxStatus{models.BoxStatusDeleted, models.BoxStatusDeleting, models.BoxStatusFailed} {
		s := activeSample(sampledAt)
		s.Status = status
		if got := classifyBoxMinute(s, minuteEnd, 120*time.Second, 5); got != "" {
			t.Errorf("status %s classified as %q, want no row", status, got)
		}
	}
}

// TestClassifyBoxMinuteBillsDetachedCPU is the `cargo build` case from the plan:
// nobody attached, nothing serving, the box is unambiguously doing paid work and the
// only witness is the CPU counter our agent reads from outside the guest.
func TestClassifyBoxMinuteBillsDetachedCPU(t *testing.T) {
	minuteEnd := time.Date(2026, 7, 30, 9, 1, 0, 0, time.UTC)
	sampledAt := minuteEnd.Add(-20 * time.Second)

	busy := 42.0
	s := idleSample(sampledAt) // the agent's own verdict is "not active"
	s.AgentCPUPercent = &busy
	if got := classifyBoxMinute(s, minuteEnd, 120*time.Second, 5); got != boxUsageKindActive {
		t.Errorf("42%% of a core classified as %q, want active", got)
	}

	// Below the threshold is the idle noise of sshd, cron and boxd. Not billable.
	quiet := 1.5
	s2 := idleSample(sampledAt)
	s2.AgentCPUPercent = &quiet
	if got := classifyBoxMinute(s2, minuteEnd, 120*time.Second, 5); got != "" {
		t.Errorf("1.5%% of a core classified as %q, want no row", got)
	}
}

// TestClassifyBoxMinuteStaleSignalsDoNotBillForever: the activity window bounds how
// long one observation can keep billing. Without it a single sample would make a box
// billable indefinitely, which is the failure mode a customer discovers on an
// invoice.
func TestClassifyBoxMinuteStaleSignalsDoNotBillForever(t *testing.T) {
	minuteEnd := time.Date(2026, 7, 30, 9, 1, 0, 0, time.UTC)
	const window = 120 * time.Second

	justInside := minuteEnd.Add(-window)
	if got := classifyBoxMinute(activeSample(justInside), minuteEnd, window, 5); got != boxUsageKindActive {
		t.Errorf("a sample exactly at the window edge classified as %q, want active", got)
	}
	justOutside := minuteEnd.Add(-window - time.Second)
	if got := classifyBoxMinute(activeSample(justOutside), minuteEnd, window, 5); got != "" {
		t.Errorf("a sample one second past the window classified as %q, want no row", got)
	}
}

// TestGuestCannotReduceBillingButCanIncreaseIt is the integrity rule, as a test.
//
// A box runs as root under the customer's own agent. If the in-guest signal were
// authoritative, a customer could under-report their usage — and, symmetrically,
// anyone could accuse us of over-reporting with no way to show otherwise. So the
// asymmetry has to hold in both directions and both are asserted here:
//
//	guest says idle  + agent says busy -> BILLED   (the guest cannot save money)
//	guest says busy  + agent says idle -> BILLED   (the guest may ask for more)
func TestGuestCannotReduceBillingButCanIncreaseIt(t *testing.T) {
	minuteEnd := time.Date(2026, 7, 30, 9, 1, 0, 0, time.UTC)
	sampledAt := minuteEnd.Add(-15 * time.Second)
	const window = 120 * time.Second

	// The out-of-guest sample says busy. There is no field a guest could set to undo
	// that — the struct has no "guest says idle" member at all, which is the
	// structural half of the guarantee. The closest thing to a guest denial is the
	// absence of a heartbeat, so that is what is asserted.
	authoritativeBusy := activeSample(sampledAt)
	authoritativeBusy.GuestHeartbeatAt = nil
	if got := classifyBoxMinute(authoritativeBusy, minuteEnd, window, 5); got != boxUsageKindActive {
		t.Errorf("agent says busy and the guest is silent: classified as %q, want active — "+
			"a guest must not be able to reduce billing by withholding its heartbeat", got)
	}

	// And the permitted direction: the guest asks to keep running while the
	// out-of-guest sample sees nothing. That is MORE billing, so it is honoured.
	guestAsks := idleSample(sampledAt)
	guestAsks.GuestHeartbeatAt = &sampledAt
	if got := classifyBoxMinute(guestAsks, minuteEnd, window, 5); got != boxUsageKindActive {
		t.Errorf("guest heartbeat with an idle agent sample: classified as %q, want active — "+
			"an in-guest signal may always ask for more billing", got)
	}
}

// TestGuestHeartbeatDefersOnlyDiscardsADenial pins the webhook-side half of the
// asymmetry: `guest_active: false` is thrown away, not stored. If it were stored, a
// later change could read it, and the only reason trusting the guest is safe is that
// there is nothing there to read.
func TestGuestHeartbeatDefersOnlyDiscardsADenial(t *testing.T) {
	no, yes := false, true
	if guestHeartbeatDefersOnly(nil) {
		t.Error("absent guest signal must not count as a heartbeat")
	}
	if guestHeartbeatDefersOnly(&no) {
		t.Error("guest_active=false must be discarded, never recorded: a guest cannot claim idleness")
	}
	if !guestHeartbeatDefersOnly(&yes) {
		t.Error("guest_active=true must record a heartbeat: asking for more billing is always allowed")
	}
}

// --- layer 2: the ledger, against a real database ---

// seedMeteredBox creates an org-owned project, a box environment and a box, and
// returns the ids the ledger tests need. Cleanup cascades from the project.
func seedMeteredBox(t *testing.T, pool *pgxpool.Pool, orgID string, status models.BoxStatus) (projectID, boxID uuid.UUID, boxName string) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, $2) RETURNING id`,
		"box-meter-"+suffix, orgID,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, projectID) })

	boxName = "bm-" + suffix
	var envID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type, runtime)
		 VALUES ($1, $2, $3, 'dev', 'box') RETURNING id`,
		projectID, boxName, boxName+"-ns",
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO boxes (project_id, environment_id, name, image, profile, status)
		 VALUES ($1, $2, $3, 'warm-v1', 'box-standard', $4) RETURNING id`,
		projectID, envID, boxName, string(status),
	).Scan(&boxID); err != nil {
		t.Fatalf("seed box: %v", err)
	}
	// box_usage carries no FK to boxes (it is a ledger and must outlive its
	// subject), so its rows are not cascaded by the project cleanup above.
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM box_usage WHERE box_id = $1`, boxID) })
	return projectID, boxID, boxName
}

// newTestBoxMeter builds a BoxMeter whose clock the caller controls. The notifier is
// nil, so nothing is mailed; the cap tests assert on the enqueued operation and the
// stamped row, which is what actually changes the customer's world.
func newTestBoxMeter(t *testing.T, pool *pgxpool.Pool, cfg *config.Config, clock *time.Time) *BoxMeter {
	t.Helper()
	pricer, err := newBoxPricer()
	if err != nil {
		t.Fatalf("newBoxPricer: %v", err)
	}
	return &BoxMeter{
		pool:   pool,
		cfg:    cfg,
		plans:  testPlansWithBoxMinutes(),
		pricer: pricer,
		now:    func() time.Time { return *clock },
	}
}

// setBoxSampleState puts the box's activity columns into an exact known state.
//
// It writes last_active_at and guest_heartbeat_at to NULL deliberately, so the ONLY
// signal in play is the out-of-guest sample. Otherwise a bumped last_active_at from
// a neighbouring minute would leak activity across the window and the test would be
// measuring the fixture rather than the meter.
func setBoxSampleState(t *testing.T, pool *pgxpool.Pool, boxID uuid.UUID, sampledAt time.Time, active bool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes
		    SET last_sample_at = $2, last_sample_active = $3, last_sample_json = NULL,
		        last_active_at = NULL, guest_heartbeat_at = NULL
		  WHERE id = $1`,
		boxID, sampledAt, active); err != nil {
		t.Fatalf("set sample state: %v", err)
	}
}

// meterMinutes runs the meter once per minute over n minutes starting at base,
// asking activeAt which of those minutes the box was busy in.
func meterMinutes(t *testing.T, pool *pgxpool.Pool, boxID uuid.UUID, m *BoxMeter, clock *time.Time,
	base time.Time, n int, activeAt func(i int) bool) {
	t.Helper()
	for i := 0; i < n; i++ {
		minuteStart := base.Add(time.Duration(i) * time.Minute)
		setBoxSampleState(t, pool, boxID, minuteStart.Add(30*time.Second), activeAt(i))
		// The meter bills the minute that has just COMPLETED, so the clock stands one
		// minute past the minute under test.
		*clock = minuteStart.Add(time.Minute)
		m.MeterBoxMinutes(context.Background())
	}
}

// ledgerRows reads the ledger for one box: minutes and money per kind.
func ledgerRows(t *testing.T, pool *pgxpool.Pool, boxID uuid.UUID) map[string]struct {
	Minutes int
	CostRub float64
} {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT kind, COUNT(*), COALESCE(SUM(cost_rub), 0)::float8 FROM box_usage
		  WHERE box_id = $1 GROUP BY kind`, boxID)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	defer rows.Close()
	out := map[string]struct {
		Minutes int
		CostRub float64
	}{}
	for rows.Next() {
		var kind string
		var minutes int
		var cost float64
		if err := rows.Scan(&kind, &minutes, &cost); err != nil {
			t.Fatalf("scan ledger: %v", err)
		}
		out[kind] = struct {
			Minutes int
			CostRub float64
		}{minutes, cost}
	}
	return out
}

// TestMeterBoxMinutes_TenActiveFiftyIdleBillsExactlyTen is the ledger form of the
// headline claim, end to end through Postgres: sixty ticks, ten of them over a busy
// box, and exactly ten rows exist afterwards.
func TestMeterBoxMinutes_TenActiveFiftyIdleBillsExactlyTen(t *testing.T) {
	pool := testOptimisticPool(t)
	cfg := boxMeterTestConfig()
	_, boxID, _ := seedMeteredBox(t, pool, "org-meter-"+uuid.NewString()[:8], models.BoxStatusReady)

	base := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	clock := base
	m := newTestBoxMeter(t, pool, cfg, &clock)

	meterMinutes(t, pool, boxID, m, &clock, base, 60, func(i int) bool { return i < 10 })

	got := ledgerRows(t, pool, boxID)
	if got[boxUsageKindActive].Minutes != 10 {
		t.Errorf("ledger holds %d active minutes, want exactly 10", got[boxUsageKindActive].Minutes)
	}
	if n := len(got); n != 1 {
		t.Errorf("ledger holds %d kinds, want only %q", n, boxUsageKindActive)
	}

	// The money is the derived per-minute price times ten. The ledger column is
	// NUMERIC(14,6), so each row is quantised to six decimals — deliberately, because
	// one active minute of a standard box is a fraction of a kopeck and rounding to 2
	// would round almost every row to zero. Six decimals leaves a residual of under
	// 1e-6 ₽ per minute, i.e. under a thousandth of a kopeck per month per box, and
	// the assertion is written against the quantised value rather than the float so a
	// future change of scale fails here instead of drifting.
	perMinute := math.Round(m.PerMinuteRub("box-standard")*1e6) / 1e6
	wantCost := perMinute * 10
	if diff := math.Abs(got[boxUsageKindActive].CostRub - wantCost); diff > 1e-9 {
		t.Errorf("ledger cost = %.9f ₽, want %.9f ₽ (10 x the per-minute price quantised to the column scale)",
			got[boxUsageKindActive].CostRub, wantCost)
	}
}

// TestMeterBoxMinutes_AnIdleHourWritesNoRowAtAll is the property migration 063 is
// built around, and it is deliberately a separate test from the one above: the claim
// is not "the idle rows sum to zero", it is that THERE ARE NO ROWS.
//
// A zero-cost row is a row a later pricing change can start charging for by editing a
// YAML file. A row that does not exist has to be created by code somebody writes and
// reviews. The difference is the entire "idle is not billed" promise.
func TestMeterBoxMinutes_AnIdleHourWritesNoRowAtAll(t *testing.T) {
	pool := testOptimisticPool(t)
	cfg := boxMeterTestConfig()
	_, boxID, _ := seedMeteredBox(t, pool, "org-meter-"+uuid.NewString()[:8], models.BoxStatusReady)

	base := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	clock := base
	m := newTestBoxMeter(t, pool, cfg, &clock)

	meterMinutes(t, pool, boxID, m, &clock, base, 60, func(int) bool { return false })

	var rowCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM box_usage WHERE box_id = $1`, boxID).Scan(&rowCount); err != nil {
		t.Fatalf("count ledger rows: %v", err)
	}
	if rowCount != 0 {
		t.Fatalf("an idle hour produced %d ledger rows, want 0 — not even a zero-cost one: "+
			"the ABSENCE of the row is what makes \"idle is not billed\" irreversible", rowCount)
	}
}

// TestMeterBoxMinutes_SleepingBoxBillsDiskOnly: a sleeping box bills zero active
// minutes and accrues its rootfs, cheaply.
func TestMeterBoxMinutes_SleepingBoxBillsDiskOnly(t *testing.T) {
	pool := testOptimisticPool(t)
	cfg := boxMeterTestConfig()
	_, boxID, _ := seedMeteredBox(t, pool, "org-meter-"+uuid.NewString()[:8], models.BoxStatusSleeping)

	base := time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC)
	clock := base
	m := newTestBoxMeter(t, pool, cfg, &clock)

	meterMinutes(t, pool, boxID, m, &clock, base, 30, func(int) bool { return true })

	got := ledgerRows(t, pool, boxID)
	if got[boxUsageKindActive].Minutes != 0 {
		t.Errorf("a sleeping box billed %d ACTIVE minutes, want 0", got[boxUsageKindActive].Minutes)
	}
	if got[boxUsageKindSuspendedDisk].Minutes != 30 {
		t.Errorf("a sleeping box accrued %d disk minutes, want 30", got[boxUsageKindSuspendedDisk].Minutes)
	}
	// Cheaply: the disk-only minute must cost strictly less than an active minute,
	// or the "asleep costs less" statement in the product is not true.
	perActive := m.PerMinuteRub("box-standard")
	perDisk := got[boxUsageKindSuspendedDisk].CostRub / 30
	if !(perDisk > 0 && perDisk < perActive) {
		t.Errorf("disk minute = %.8f ₽, active minute = %.8f ₽; want 0 < disk < active", perDisk, perActive)
	}
}

// TestMeterBoxMinutes_IsIdempotentOnThePrimaryKey: the meter runs unguarded on every
// replica, so a re-run is the NORMAL case, not an edge case.
//
// Two replays are asserted, because they fail differently: re-running the same tick
// must not add a row (ON CONFLICT), and it must not change the row's price either
// (DO NOTHING rather than DO UPDATE) — a ledger whose rows can be rewritten cannot
// settle an argument about a bill.
func TestMeterBoxMinutes_IsIdempotentOnThePrimaryKey(t *testing.T) {
	pool := testOptimisticPool(t)
	cfg := boxMeterTestConfig()
	_, boxID, _ := seedMeteredBox(t, pool, "org-meter-"+uuid.NewString()[:8], models.BoxStatusReady)

	base := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	clock := base
	m := newTestBoxMeter(t, pool, cfg, &clock)

	meterMinutes(t, pool, boxID, m, &clock, base, 5, func(int) bool { return true })
	first := ledgerRows(t, pool, boxID)
	if first[boxUsageKindActive].Minutes != 5 {
		t.Fatalf("first pass billed %d minutes, want 5", first[boxUsageKindActive].Minutes)
	}

	// Replay the identical five ticks, twice, exactly as a restarted pod and a second
	// replica would.
	meterMinutes(t, pool, boxID, m, &clock, base, 5, func(int) bool { return true })
	meterMinutes(t, pool, boxID, m, &clock, base, 5, func(int) bool { return true })

	again := ledgerRows(t, pool, boxID)
	if again[boxUsageKindActive].Minutes != 5 {
		t.Errorf("after two replays the ledger holds %d minutes, want still 5", again[boxUsageKindActive].Minutes)
	}
	if again[boxUsageKindActive].CostRub != first[boxUsageKindActive].CostRub {
		t.Errorf("a replay rewrote the price: %.6f -> %.6f. The first verdict must stand",
			first[boxUsageKindActive].CostRub, again[boxUsageKindActive].CostRub)
	}

	// Tamper with a stored price and replay: the row must survive untouched. This is
	// the DO NOTHING/DO UPDATE distinction stated as a property rather than as a SQL
	// keyword.
	if _, err := pool.Exec(context.Background(),
		`UPDATE box_usage SET cost_rub = 999 WHERE box_id = $1 AND minute_start = $2`,
		boxID, base); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	meterMinutes(t, pool, boxID, m, &clock, base, 5, func(int) bool { return true })
	var tampered float64
	if err := pool.QueryRow(context.Background(),
		`SELECT cost_rub::float8 FROM box_usage WHERE box_id = $1 AND minute_start = $2`,
		boxID, base).Scan(&tampered); err != nil {
		t.Fatalf("read tampered row: %v", err)
	}
	if tampered != 999 {
		t.Errorf("the replay overwrote an existing ledger row (cost_rub is now %.2f); "+
			"the insert must be ON CONFLICT DO NOTHING so history is append-only", tampered)
	}
}

// TestMeterBoxMinutes_GuestDenialDoesNotReduceTheLedger is the integrity rule
// end to end: the guest reports inactivity through the real webhook while the
// out-of-guest sample says the box is busy, and the minute is billed anyway.
//
// Asserted through the ingest path rather than on the classifier alone, because the
// place a guest could realistically influence billing is the webhook body, and the
// property that has to hold is that nothing it sends is stored in a form the meter
// can read as a denial.
func TestMeterBoxMinutes_GuestDenialDoesNotReduceTheLedger(t *testing.T) {
	pool := testOptimisticPool(t)
	cfg := boxMeterTestConfig()
	h := &Handler{pool: pool, cfg: cfg}
	_, boxID, ref := seedBoxWithInstanceRef(t, pool, models.BoxStatusReady)

	// The agent (outside the guest) says busy; the guest claims it is doing nothing.
	body := `{"instance_ref":"` + ref + `","active":true,"guest_active":false,"sample":{"cpu_percent":0}}`
	c, rec := newWebhookCtx(t, "Bearer ok", body)
	h.boxAgentSampleWebhook(c, fakeVerifier{claims: boxAgentClaims()})
	if rec.Code != http.StatusOK {
		t.Fatalf("sample ingest: code = %d; body=%s", rec.Code, rec.Body.String())
	}

	var sampleActive bool
	var guestHeartbeat *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT last_sample_active, guest_heartbeat_at FROM boxes WHERE id = $1`, boxID,
	).Scan(&sampleActive, &guestHeartbeat); err != nil {
		t.Fatalf("read box: %v", err)
	}
	if !sampleActive {
		t.Error("the out-of-guest verdict was not stored as active; a guest denial must not overwrite it")
	}
	if guestHeartbeat != nil {
		t.Error("guest_active=false wrote a heartbeat stamp; a denial must be discarded entirely")
	}

	// And the meter bills the minute. The clock is placed just past the sample so the
	// freshness window covers it.
	clock := time.Now().UTC().Add(time.Minute)
	m := newTestBoxMeter(t, pool, cfg, &clock)
	m.MeterBoxMinutes(context.Background())

	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM box_usage WHERE box_id = $1`, boxID) })
	var billed int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM box_usage WHERE box_id = $1 AND kind = $2`,
		boxID, boxUsageKindActive).Scan(&billed); err != nil {
		t.Fatalf("count billed minutes: %v", err)
	}
	if billed == 0 {
		t.Error("a guest claiming inactivity reduced the ledger to zero billed minutes; " +
			"the out-of-guest sample is the authoritative signal and must win")
	}
}

// --- the spend cap ---

// seedBoxUsage writes n ledger minutes of the given kind at the given per-minute
// cost, ending just before endsAt. Used to put a box at a known spend without
// running the meter for hours.
func seedBoxUsage(t *testing.T, pool *pgxpool.Pool, boxID, projectID uuid.UUID, orgID, kind string, n int, costEach float64, endsAt time.Time) {
	t.Helper()
	for i := 0; i < n; i++ {
		minute := endsAt.Add(-time.Duration(i+1) * time.Minute)
		if _, err := pool.Exec(context.Background(),
			`INSERT INTO box_usage (box_id, minute_start, kind, org_id, project_id, cost_rub)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (box_id, minute_start, kind) DO NOTHING`,
			boxID, minute, kind, orgID, projectID, costEach); err != nil {
			t.Fatalf("seed usage: %v", err)
		}
	}
}

// countBoxOperations counts enqueued operations of one action for one box name.
func countBoxOperations(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID, boxName, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM operations
		  WHERE project_id = $1 AND resource_name = $2 AND action = $3`,
		projectID, boxName, action).Scan(&n); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	return n
}

// TestSpendCap_WarnsThenSuspendsAndNeverDeletes walks the two non-destructive
// thresholds in order and asserts the shape of each action.
//
// The load-bearing assertion is the negative one: at the cap the box is SUSPENDED and
// no DeleteBox is enqueued. A runaway must be able to cost a customer money and never
// their data — that is why the cap exists at all, and a cap that deleted would be
// strictly worse than no cap, because the customer would have preferred the bill.
func TestSpendCap_WarnsThenSuspendsAndNeverDeletes(t *testing.T) {
	pool := testOptimisticPool(t)
	orgID := "org-cap-" + uuid.NewString()[:8]
	projectID, boxID, boxName := seedMeteredBox(t, pool, orgID, models.BoxStatusReady)

	cfg := boxMeterTestConfig()
	const capRub = 10.0
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET spend_cap_rub = $2 WHERE id = $1`, boxID, capRub); err != nil {
		t.Fatalf("set cap: %v", err)
	}

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	clock := now
	m := newTestBoxMeter(t, pool, cfg, &clock)

	// (1) 85% of the cap: one warning, nothing suspended.
	seedBoxUsage(t, pool, boxID, projectID, orgID, boxUsageKindActive, 85, 0.1, now)
	m.MeterBoxMinutes(context.Background())

	var warnedAt, cappedAt *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT spend_cap_warned_at, spend_capped_at FROM boxes WHERE id = $1`, boxID,
	).Scan(&warnedAt, &cappedAt); err != nil {
		t.Fatalf("read cap stamps: %v", err)
	}
	if warnedAt == nil {
		t.Error("at 85% of the cap no warning was stamped; the customer gets no chance to act")
	}
	if cappedAt != nil {
		t.Error("at 85% of the cap the box was already stopped")
	}
	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionSuspendBox); n != 0 {
		t.Errorf("at 85%% of the cap %d suspends were enqueued, want 0", n)
	}

	// (2) Over the cap: suspended, stamped, and NOT deleted.
	seedBoxUsage(t, pool, boxID, projectID, orgID, boxUsageKindActive, 30, 0.1, now.Add(-90*time.Minute))
	m.MeterBoxMinutes(context.Background())

	if err := pool.QueryRow(context.Background(),
		`SELECT spend_capped_at FROM boxes WHERE id = $1`, boxID).Scan(&cappedAt); err != nil {
		t.Fatalf("read spend_capped_at: %v", err)
	}
	if cappedAt == nil {
		t.Fatal("over the cap the box was not stamped spend_capped_at")
	}
	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionSuspendBox); n != 1 {
		t.Errorf("over the cap %d suspends were enqueued, want exactly 1", n)
	}
	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionDeleteBox); n != 0 {
		t.Errorf("over the cap %d DELETES were enqueued, want 0 — the cap suspends, never deletes: "+
			"the customer's data must survive their own runaway", n)
	}

	// The suspend payload names the cause, so the customer's email and
	// dada_box_spend_cap_hits_total agree about why the platform acted.
	var payload []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT payload FROM operations
		  WHERE project_id = $1 AND resource_name = $2 AND action = $3`,
		projectID, boxName, models.ActionSuspendBox).Scan(&payload); err != nil {
		t.Fatalf("read suspend payload: %v", err)
	}
	var sp models.SuspendBoxPayload
	if err := json.Unmarshal(payload, &sp); err != nil {
		t.Fatalf("decode suspend payload: %v", err)
	}
	if sp.Reason != "spend_cap" {
		t.Errorf("suspend reason = %q, want \"spend_cap\"", sp.Reason)
	}
}

// TestSpendCap_StopIsIrreversibleWithoutRaisingTheCap: a stop that a resume could
// undo is a dialog box, not a spend cap.
//
// Two halves, and the second is what makes the feature usable rather than merely
// strict: repeated ticks keep the stop in place and do NOT pile up suspend
// operations, and raising the cap — the deliberate act — lifts it.
func TestSpendCap_StopIsIrreversibleWithoutRaisingTheCap(t *testing.T) {
	pool := testOptimisticPool(t)
	orgID := "org-cap-" + uuid.NewString()[:8]
	projectID, boxID, boxName := seedMeteredBox(t, pool, orgID, models.BoxStatusReady)

	cfg := boxMeterTestConfig()
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET spend_cap_rub = 10 WHERE id = $1`, boxID); err != nil {
		t.Fatalf("set cap: %v", err)
	}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	clock := now
	m := newTestBoxMeter(t, pool, cfg, &clock)

	seedBoxUsage(t, pool, boxID, projectID, orgID, boxUsageKindActive, 120, 0.1, now)
	m.MeterBoxMinutes(context.Background())

	// Pretend the worker suspended it and the customer resumed it by hand — the
	// lifecycle path that must NOT clear the cap.
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET status = 'Ready' WHERE id = $1`, boxID); err != nil {
		t.Fatalf("resume: %v", err)
	}
	for i := 0; i < 3; i++ {
		clock = clock.Add(time.Minute)
		m.MeterBoxMinutes(context.Background())
	}
	var cappedAt *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT spend_capped_at FROM boxes WHERE id = $1`, boxID).Scan(&cappedAt); err != nil {
		t.Fatalf("read spend_capped_at: %v", err)
	}
	if cappedAt == nil {
		t.Error("a plain resume cleared the spend cap stop; only raising the cap may do that")
	}
	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionSuspendBox); n != 1 {
		t.Errorf("%d suspends enqueued across four ticks, want exactly 1: a capped box must not be "+
			"re-suspended every minute", n)
	}

	// The operator/customer action: raise the cap above the accrued spend.
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET spend_cap_rub = 1000 WHERE id = $1`, boxID); err != nil {
		t.Fatalf("raise cap: %v", err)
	}
	clock = clock.Add(time.Minute)
	m.MeterBoxMinutes(context.Background())
	if err := pool.QueryRow(context.Background(),
		`SELECT spend_capped_at FROM boxes WHERE id = $1`, boxID).Scan(&cappedAt); err != nil {
		t.Fatalf("read spend_capped_at: %v", err)
	}
	if cappedAt != nil {
		t.Error("raising the cap above the accrued spend did not lift the stop; the customer would be " +
			"permanently stuck with a box they have already paid to keep running")
	}
}

// TestSpendCap_DiskAccrualWarnsBeforeItDeletes is the only destructive branch in the
// meter, and the test's whole point is the ORDER: a warning, then a grace period,
// then the delete. A delete on the same tick as the warning would be a notification
// that something had already happened.
func TestSpendCap_DiskAccrualWarnsBeforeItDeletes(t *testing.T) {
	pool := testOptimisticPool(t)
	orgID := "org-cap-" + uuid.NewString()[:8]
	projectID, boxID, boxName := seedMeteredBox(t, pool, orgID, models.BoxStatusSleeping)

	cfg := boxMeterTestConfig()
	if _, err := pool.Exec(context.Background(),
		`UPDATE boxes SET spend_cap_rub = 10, slept_at = now() WHERE id = $1`, boxID); err != nil {
		t.Fatalf("set cap: %v", err)
	}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	clock := now
	m := newTestBoxMeter(t, pool, cfg, &clock)

	// Disk accrual alone at 2.5x the cap.
	seedBoxUsage(t, pool, boxID, projectID, orgID, boxUsageKindSuspendedDisk, 250, 0.1, now)

	m.MeterBoxMinutes(context.Background())
	var deleteWarnedAt *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT spend_cap_delete_warned_at FROM boxes WHERE id = $1`, boxID).Scan(&deleteWarnedAt); err != nil {
		t.Fatalf("read delete warning stamp: %v", err)
	}
	if deleteWarnedAt == nil {
		t.Fatal("disk accrual past the limit did not warn")
	}
	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionDeleteBox); n != 0 {
		t.Fatalf("%d deletes enqueued on the same tick as the warning, want 0", n)
	}

	// Inside the grace period: still nothing.
	clock = now.Add(boxSpendCapDeleteGrace - time.Hour)
	m.MeterBoxMinutes(context.Background())
	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionDeleteBox); n != 0 {
		t.Errorf("%d deletes enqueued inside the grace period, want 0", n)
	}

	// Past it: destroyed, once.
	clock = now.Add(boxSpendCapDeleteGrace + time.Minute)
	m.MeterBoxMinutes(context.Background())
	if n := countBoxOperations(t, pool, projectID, boxName, models.ActionDeleteBox); n != 1 {
		t.Errorf("%d deletes enqueued after the grace period, want exactly 1", n)
	}
}

// --- the box_minutes quota, through the existing gate ---

// boxQuotaTestClock is a fixed instant safely inside a calendar month, for tests
// that inject Handler.now. countOrgBoxMinutes and GetBoxUsage both window on
// monthStart(now); a fixture built from a real time.Now() reach-backs 12 to 500+
// minutes into the past, which crosses into the previous month whenever the test
// happens to run in the first few hours after a UTC month rollover, failing for a
// reason that has nothing to do with the code under test.
func boxQuotaTestClock() time.Time {
	return time.Date(2026, time.June, 15, 12, 0, 0, 0, time.UTC)
}

// testPlansWithBoxMinutes is testPlans() with a box-minute allowance, small enough
// that a fixture can exhaust it.
func testPlansWithBoxMinutes() []pricing.Plan {
	plans := testPlans()
	for i := range plans {
		switch plans[i].Key {
		case "free":
			plans[i].Quotas.BoxMinutes = 30
		case "startup":
			plans[i].Quotas.BoxMinutes = 3000
		}
	}
	return plans
}

// TestCreateBox_BoxMinutesQuotaUsesTheExistingForbiddenShape: the box gate must not
// invent an error contract.
//
// The assertion is byte-level on purpose. storage_cap_test.go already pins
// {error: "quota_exceeded", resource, limit, upgrade, message} for apps and storage,
// and a client that handles that wall for apps has to handle it for boxes with no
// change at all. A box-specific 402, or a 403 with a different key, would be a second
// contract for the same event.
func TestCreateBox_BoxMinutesQuotaUsesTheExistingForbiddenShape(t *testing.T) {
	pool := testOptimisticPool(t)
	orgID := "org-boxq-" + uuid.NewString()[:8]
	seedFreePlanOrg(t, pool, orgID)

	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, $2) RETURNING id`,
		"box-quota-"+suffix, orgID,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, projectID) })

	fixedNow := boxQuotaTestClock()
	h := &Handler{
		pool:         pool,
		cfg:          &config.Config{BillingEnabled: true},
		billingPlans: testPlansWithBoxMinutes(),
		now:          func() time.Time { return fixedNow },
	}
	claims := godClaims(seedUser(t, pool))

	c, rec := newBoxCtx(t, http.MethodPost, `{}`, boxParams(projectID, ""), claims)
	h.CreateBox(c)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("first box: code = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	billedBox := uuid.New()
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM box_usage WHERE box_id = $1`, billedBox) })
	seedBoxUsage(t, pool, billedBox, projectID, orgID, boxUsageKindActive, 30, 0.05, fixedNow)

	c2, rec2 := newBoxCtx(t, http.MethodPost, `{}`, boxParams(projectID, ""), claims)
	h.CreateBox(c2)
	assertBoxQuotaExceeded(t, rec2.Code, rec2.Body.Bytes(), 30)
}

// TestBoxMinutesQuotaIgnoresSleepingDiskAccrual: a customer's minute allowance must
// not be consumed while they are not using anything.
//
// suspended_disk rows are money (and the per-box spend cap enforces them), but they
// are not minutes of USE. Counting them here would mean a box left asleep quietly ate
// the allowance — precisely the bill-shock "idle is not billed" rules out.
func TestBoxMinutesQuotaIgnoresSleepingDiskAccrual(t *testing.T) {
	pool := testOptimisticPool(t)
	orgID := "org-boxq-" + uuid.NewString()[:8]
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, $2) RETURNING id`,
		"box-quota-disk-"+suffix, orgID,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, projectID) })

	boxID := uuid.New()
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM box_usage WHERE box_id = $1`, boxID) })
	now := boxQuotaTestClock()
	seedBoxUsage(t, pool, boxID, projectID, orgID, boxUsageKindSuspendedDisk, 500, 0.001, now)
	seedBoxUsage(t, pool, boxID, projectID, orgID, boxUsageKindActive, 7, 0.05, now.Add(-time.Hour))

	got, err := countOrgBoxMinutes(ctx, pool, orgID, now)
	if err != nil {
		t.Fatalf("countOrgBoxMinutes: %v", err)
	}
	if got != 7 {
		t.Fatalf("counted %d box minutes, want 7: 500 minutes of sleeping-disk accrual are money, "+
			"not minutes of use, and must not consume a customer's allowance", got)
	}
}

// assertBoxQuotaExceeded pins the shared quota-wall body for box_minutes.
func assertBoxQuotaExceeded(t *testing.T, code int, body []byte, wantLimit float64) {
	t.Helper()
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", code, body)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if parsed["error"] != "quota_exceeded" {
		t.Errorf("error = %v, want quota_exceeded", parsed["error"])
	}
	if parsed["resource"] != "box_minutes" {
		t.Errorf("resource = %v, want box_minutes", parsed["resource"])
	}
	if limit, _ := parsed["limit"].(float64); limit != wantLimit {
		t.Errorf("limit = %v, want %v", parsed["limit"], wantLimit)
	}
	if parsed["upgrade"] != true {
		t.Errorf("upgrade = %v, want true (the field the console reads to show the upgrade prompt)", parsed["upgrade"])
	}
}

// --- the read endpoint ---

// TestGetBoxUsage_ReportsActualBasisAndTheWindow: the endpoint is a window over the
// ledger, and it says so — basis "actual", never an estimate, because every row was
// written when the minute elapsed with its price frozen into it.
func TestGetBoxUsage_ReportsActualBasisAndTheWindow(t *testing.T) {
	pool := testOptimisticPool(t)
	orgID := "org-usage-" + uuid.NewString()[:8]
	projectID, boxID, boxName := seedMeteredBox(t, pool, orgID, models.BoxStatusReady)
	now := boxQuotaTestClock()
	h := &Handler{
		pool:         pool,
		cfg:          boxMeterTestConfig(),
		billingPlans: testPlansWithBoxMinutes(),
		now:          func() time.Time { return now },
	}
	claims := godClaims(seedUser(t, pool))

	seedBoxUsage(t, pool, boxID, projectID, orgID, boxUsageKindActive, 12, 0.05, now)
	seedBoxUsage(t, pool, boxID, projectID, orgID, boxUsageKindSuspendedDisk, 4, 0.001, now)

	c, rec := newBoxCtx(t, http.MethodGet, "", boxParams(projectID, boxName), claims)
	h.GetBoxUsage(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Basis         string  `json:"basis"`
		Currency      string  `json:"currency"`
		ActiveMinutes int     `json:"active_minutes"`
		BilledMinutes int     `json:"billed_minutes"`
		TotalRub      float64 `json:"total_rub"`
		Kinds         []struct {
			Kind    string  `json:"kind"`
			Minutes int     `json:"minutes"`
			CostRub float64 `json:"cost_rub"`
		} `json:"kinds"`
		Period struct {
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"period"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Basis != basisActual {
		t.Errorf("basis = %q, want %q: box cost is metered, never modelled", resp.Basis, basisActual)
	}
	if resp.Currency != "RUB" {
		t.Errorf("currency = %q, want RUB", resp.Currency)
	}
	if resp.ActiveMinutes != 12 {
		t.Errorf("active_minutes = %d, want 12", resp.ActiveMinutes)
	}
	if resp.BilledMinutes != 16 {
		t.Errorf("billed_minutes = %d, want 16 (12 active + 4 sleeping-disk)", resp.BilledMinutes)
	}
	if len(resp.Kinds) != 2 {
		t.Errorf("kinds = %v, want one entry per billed kind", resp.Kinds)
	}
	if resp.Period.Start == "" || resp.Period.End == "" {
		t.Error("the window is not reported; a usage figure without its window is not checkable")
	}
}

// TestGetBoxUsage_RejectsAnUnboundedWindow: the ledger is one row per minute per box,
// so an unbounded window is an unbounded scan. A 400 that names the limit is better
// than a request that eventually succeeds after locking a table.
func TestGetBoxUsage_RejectsAnUnboundedWindow(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	if _, _, err := parseBoxUsageWindow(now.Add(-boxUsageMaxWindow-time.Hour).Format(time.RFC3339),
		now.Format(time.RFC3339), now); err == nil {
		t.Error("a window longer than the cap was accepted")
	}
	if _, _, err := parseBoxUsageWindow(now.Format(time.RFC3339),
		now.Add(-time.Hour).Format(time.RFC3339), now); err == nil {
		t.Error("an inverted window was accepted")
	}
	// Unix seconds and RFC3339 are both accepted, because the console speaks one and
	// the metrics endpoints in this codebase already speak the other.
	from, to, err := parseBoxUsageWindow(fmt.Sprint(now.Add(-time.Hour).Unix()), fmt.Sprint(now.Unix()), now)
	if err != nil {
		t.Fatalf("unix seconds rejected: %v", err)
	}
	if to.Sub(from) != time.Hour {
		t.Errorf("unix window = %v, want 1h", to.Sub(from))
	}
	// The default is the current calendar month, so a caller who passes nothing gets
	// the figure that matches their invoice preview.
	from, to, err = parseBoxUsageWindow("", "", now)
	if err != nil {
		t.Fatalf("default window: %v", err)
	}
	if !from.Equal(monthStart(now)) || !to.Equal(now) {
		t.Errorf("default window = %v..%v, want %v..%v", from, to, monthStart(now), now)
	}
}

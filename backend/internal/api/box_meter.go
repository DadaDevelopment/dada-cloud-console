package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/dada-tuda/console/backend/internal/billing"
	"github.com/dada-tuda/console/backend/internal/billing/costengine"
	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/dada-tuda/console/backend/internal/boxcatalog"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/metrics"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// Dada Box per-minute metering.
//
// NO ADVISORY LOCK, deliberately, and this is the one structural decision in the
// file. advisory_lock.go's own comment divides the background loops in two:
// non-idempotent ones (DNS writes, alert mail, ActionSets) take a lock so exactly
// one replica fires per tick, and idempotent ones run unguarded on every replica.
// This loop is in the second group BY CONSTRUCTION, not by hope: every write is an
// INSERT on the (box_id, minute_start, kind) primary key with ON CONFLICT DO
// NOTHING, so two racing replicas, a retried tick and a pod that died halfway
// through all converge on the same one row per minute.
//
// The alternative — a lock — would make a single replica's outage into silent
// revenue loss, because the minutes it failed to meter are gone: there is no
// backfill for "was this box busy 40 minutes ago", since the signal is a sample
// stream and not a counter we can re-read. Redundancy is worth more here than
// exclusivity, and the PK is what buys it.
//
// The spend cap is the one non-idempotent thing in the tick (it enqueues an
// operation and sends mail), so it is guarded by state rather than by a lock:
// boxes.spend_capped_at / spend_cap_warned_at are stamped with a conditional
// UPDATE whose row count decides whether this replica is the one that acts. Two
// replicas therefore cannot both suspend or both email.

// Box usage ledger kinds. These two strings are the whole vocabulary the meter
// writes into box_usage.kind; migration 063 documents why the column carries no
// CHECK constraint.
const (
	// boxUsageKindActive is one minute in which the box was doing the customer's
	// work: an attached session, guest CPU over the threshold, a request served on
	// the exposed endpoint, or an operation in flight.
	boxUsageKindActive = "active"
	// boxUsageKindSuspendedDisk is one minute in which the box was ASLEEP and
	// therefore consuming no CPU or memory — but its rootfs is still occupying our
	// storage. Billing it separately and cheaply is what keeps "idle is free"
	// honest: claiming a 40 GiB disk costs nothing while it sits on our fleet for
	// 72 hours would be the kind of leaked promise that makes every other number
	// suspect. It is a different KIND rather than a discount on the active rate so
	// the two can never be confused in a sum or in an invoice line.
	boxUsageKindSuspendedDisk = "suspended_disk"
)

// boxSpendCapWarnRatio is the fraction of the cap at which the customer gets one
// heads-up email. 0.8 leaves enough room to raise the cap or finish the task
// before anything is suspended; a warning at 0.99 would arrive after the decision
// it exists to enable.
const boxSpendCapWarnRatio = 0.8

// boxSpendCapDiskDeleteMultiple is the suspended-disk accrual, as a multiple of
// the cap, at which the box is destroyed rather than left asleep forever.
//
// It exists because "suspend, never delete" has a limit that has to be named: a
// suspended box keeps costing us storage, and a customer who never comes back
// would otherwise be handed unbounded free disk in the name of protecting their
// data. 2x the cap, reached by DISK accrual alone (not total spend), means the
// box has been asleep for roughly twice as long as its entire compute budget
// would have lasted. And it never fires silently — the warning email goes first
// and boxSpendCapDeleteGrace has to elapse.
const boxSpendCapDiskDeleteMultiple = 2.0

// boxSpendCapDeleteGrace is how long after the destroy-warning email the box
// survives regardless. A day, so a warning sent while somebody is asleep is still
// a warning and not a notification of something already done.
const boxSpendCapDeleteGrace = 24 * time.Hour

// boxPricer turns a box footprint into rubles per minute.
//
// NO PRICE TABLE, on purpose (decision D5). The unit costs come from the box
// capacity pool's own share of the hardware bill (box-fleet-cost.yaml,
// deliberately not cluster-cost.yaml — see billing.LoadBoxFleetCost) and the
// customer-facing figure
// is that internal cost times pricing.MarkupDefault, which is the identical
// treatment pricing.PriceFloor gives a monthly plan. Because
// costengine.PerMinuteCost divides by a fixed 43200, a box billed for a whole
// month costs exactly what the same footprint costs as a month of VM — the "a box
// minute matches a VPS" claim is arithmetic, and there is no second place where a
// price could drift away from it.
type boxPricer struct {
	unit   costengine.UnitCost
	markup float64
}

// newBoxPricer loads the box capacity-pool cost and derives its per-unit monthly costs.
// Fails closed: a broken or missing fleet cost file is an error rather than a
// silent zero, because a zero unit cost would meter every minute at 0.00 ₽ and
// the ledger would look healthy while billing nothing.
func newBoxPricer() (boxPricer, error) {
	fleet, err := billing.LoadBoxFleetCost("")
	if err != nil {
		return boxPricer{}, err
	}
	unit, err := costengine.ComputeUnitCost(fleet)
	if err != nil {
		return boxPricer{}, err
	}
	return boxPricer{unit: unit, markup: pricing.MarkupDefault}, nil
}

// footprintFor returns the resources one minute of the given kind consumes for a
// box of the given catalog profile.
//
// An unknown profile falls back to the catalog default rather than to zero: a
// profile that vanished from the catalog between a box's creation and this tick
// (a deploy that dropped a size) must not silently make that box free.
func (p boxPricer) footprintFor(profile, kind string) costengine.Footprint {
	size, found := boxcatalog.LookupSize(profile)
	if !found {
		size = boxcatalog.DefaultSize()
	}
	if kind == boxUsageKindSuspendedDisk {
		// Asleep: no CPU, no resident memory, only the rootfs.
		return costengine.Footprint{StorageGB: float64(size.DiskGB)}
	}
	return costengine.Footprint{
		VCPU:      float64(size.VCPU),
		RAMGB:     float64(size.MemoryMB) / 1024,
		StorageGB: float64(size.DiskGB),
	}
}

// perMinuteRub is the CUSTOMER-FACING price of one minute of this footprint: the
// derived internal cost times the markup. This is the number written to
// box_usage.cost_rub and summed against boxes.spend_cap_rub, because a spend cap
// is a statement about the customer's money, not about our hardware bill.
func (p boxPricer) perMinuteRub(fp costengine.Footprint) float64 {
	return costengine.PerMinuteCost(fp, p.unit) * p.markup
}

// BoxMeter is the per-minute metering tick and the spend-cap enforcer.
//
// Constructed once at startup so the fleet cost file is parsed once and a broken
// one is a loud startup failure instead of a warning logged every 60 seconds.
type BoxMeter struct {
	pool     *pgxpool.Pool
	cfg      *config.Config
	plans    []pricing.Plan
	pricer   boxPricer
	notifier *notify.Notifier
	// operatorEmail receives the copy when a box has no resolvable owner, so a cap
	// event is never dropped into the void (the same rule the app watchers follow).
	operatorEmail string
	// now is the injected clock. Metering is arithmetic on minute boundaries, so a
	// test that cannot choose "now" can only assert on wall-clock coincidences.
	now func() time.Time
}

// NewBoxMeter builds the metering tick. Returns an error only when the box fleet
// cost cannot be loaded or is internally inconsistent.
func NewBoxMeter(pool *pgxpool.Pool, cfg *config.Config, plans []pricing.Plan, notifier *notify.Notifier) (*BoxMeter, error) {
	pricer, err := newBoxPricer()
	if err != nil {
		return nil, err
	}
	return &BoxMeter{
		pool:          pool,
		cfg:           cfg,
		plans:         plans,
		pricer:        pricer,
		notifier:      notifier,
		operatorEmail: cfg.AuditNotifyEmail,
		now:           func() time.Time { return time.Now().UTC() },
	}, nil
}

// UnitCost exposes the derived per-unit monthly costs of the box fleet, for the
// startup log line and for scripts/box-unit-cost-check.sh's counterpart in Go
// tests. Read-only.
func (m *BoxMeter) UnitCost() costengine.UnitCost { return m.pricer.unit }

// PerMinuteRub is the customer-facing price of one active minute of the named
// profile. Exposed so the box catalog surface and the tests quote the same number
// the ledger writes, rather than a second copy of the arithmetic.
func (m *BoxMeter) PerMinuteRub(profile string) float64 {
	return m.pricer.perMinuteRub(m.pricer.footprintFor(profile, boxUsageKindActive))
}

// boxActivitySignals is everything the meter knows about one box for one minute.
//
// It is a value type with no database in it so the activity RULE can be tested as
// arithmetic — which matters more here than usual, because the rule is what the
// customer is charged by and "10 active minutes out of 60 bills exactly 10" has to
// be provable without a fixture.
type boxActivitySignals struct {
	Status models.BoxStatus

	// AgentSampleAt / AgentSampleActive are THE AUTHORITATIVE SIGNAL: a sample
	// taken by our box-agent from OUTSIDE the guest. Everything else in this
	// struct can only add to what these say.
	AgentSampleAt     *time.Time
	AgentSampleActive bool
	// AgentCPUPercent is guest CPU as a percentage of one core, as observed from
	// outside the guest (cgroup counters, not a number the guest reports about
	// itself). nil when the sample carried no cpu field.
	AgentCPUPercent *float64

	// GuestHeartbeatAt is the IN-GUEST heartbeat, and it is trusted in exactly one
	// direction. See the file-level comment on boxGuestHeartbeatAsymmetry.
	GuestHeartbeatAt *time.Time

	// TouchedAt is boxes.last_active_at: the recency stamp bumped when an
	// authenticated session attaches, when the exposed endpoint serves a request,
	// and when the runtime reports Ready/Idle. It is a union of activity sources
	// that are individually recorded elsewhere; treating it as one signal is what
	// lets the session and expose paths (phases 2 and 4) start contributing to
	// billing without a change here.
	TouchedAt *time.Time

	// OperationInFlight is true when a non-terminal box operation was recently
	// advanced for this box. "Recently" is bounded on purpose (see the query in
	// loadBoxMeterRows): an operation row stuck in Created forever must not bill
	// forever.
	OperationInFlight bool
}

// boxGuestHeartbeatAsymmetry documents, next to the code that enforces it, the
// integrity rule the whole metering design turns on:
//
//	THE GUEST IS NOT A SOURCE OF BILLING TRUTH. A box runs as root under the
//	customer's own agent. If an in-guest heartbeat were authoritative, a customer
//	could under-report their usage, and — symmetrically and much worse for us —
//	anyone could accuse us of over-reporting, with no way to show otherwise.
//
//	So the in-guest signal is admitted in ONE direction only: it can mark a minute
//	ACTIVE (asking for more billing) and it can defer suspension. It can never
//	mark a minute idle, and it can never veto the out-of-guest sample. Trusting a
//	signal that can only cost its own sender money is safe; trusting one that can
//	save its sender money is not.
//
// The enforcement is structural rather than conditional: classifyBoxMinute ORs the
// guest heartbeat into the activity decision and there is no branch anywhere that
// reads it as a negative. A future change that tried to would have to add one, and
// that is a reviewable diff.
const boxGuestHeartbeatAsymmetry = "in-guest signals may only increase billing"

// classifyBoxMinute decides what to bill for one box for one minute.
//
// Returns boxUsageKindActive, boxUsageKindSuspendedDisk, or "" — and "" means NO
// ROW IS WRITTEN, which is the point (migration 063): the absence of the row is
// the "not billed" statement, and it is the only version of that statement a later
// pricing change cannot quietly reverse.
//
// minuteEnd is the exclusive end of the metered minute; a signal counts when it is
// no older than window before that instant. The window (BOX_ACTIVE_WINDOW_SECS,
// two ticks by default) makes the rule biased toward billing on missing data: the
// samples come from our own agent over our own network, so a dropped one is our
// packet loss, and treating it as "the customer went idle" would let a flaky link
// hand out free compute.
func classifyBoxMinute(s boxActivitySignals, minuteEnd time.Time, window time.Duration, activeCPUPercent float64) string {
	// Tombstones and bodies being torn down are not billable at all: there is
	// nothing left to consume, and a Failed box never gave the customer anything.
	switch s.Status {
	case models.BoxStatusDeleted, models.BoxStatusDeleting, models.BoxStatusFailed:
		return ""
	case models.BoxStatusSleeping:
		// Asleep: compute billing stops here, storage does not. No activity signal
		// can make a sleeping box active — waking it is a ResumeBox operation, and
		// that operation is itself an activity signal on the minute it runs, at
		// which point the box is no longer Sleeping.
		return boxUsageKindSuspendedDisk
	}

	fresh := func(ts *time.Time) bool {
		return ts != nil && !ts.Before(minuteEnd.Add(-window))
	}

	switch {
	// (a) The authoritative out-of-guest verdict.
	case s.AgentSampleActive && fresh(s.AgentSampleAt):
		return boxUsageKindActive
	// (b) Guest CPU over the threshold, measured from outside the guest. This is
	// what bills a detached `cargo build` or a model download: nobody is attached,
	// nothing is serving, and the box is unambiguously doing paid work.
	case s.AgentCPUPercent != nil && *s.AgentCPUPercent > activeCPUPercent && fresh(s.AgentSampleAt):
		return boxUsageKindActive
	// (c) An attached session, a request served by the exposed endpoint, or a
	// runtime phase report. See boxActivitySignals.TouchedAt.
	case fresh(s.TouchedAt):
		return boxUsageKindActive
	// (d) A box operation in flight. Booting, attaching a database and
	// crystallizing all occupy the body whether or not anyone is typing.
	case s.OperationInFlight:
		return boxUsageKindActive
	// (e) The in-guest heartbeat, admitted LAST and only positively — see
	// boxGuestHeartbeatAsymmetry. It cannot appear in any negative position in this
	// function, which is what makes the asymmetry structural.
	case fresh(s.GuestHeartbeatAt):
		return boxUsageKindActive
	}
	// Idle. No row.
	return ""
}

// boxMeterRow is one box as the metering query returns it.
type boxMeterRow struct {
	BoxID         uuid.UUID
	ProjectID     uuid.UUID
	OrgID         string
	Profile       string
	Plan          string
	Signals       boxActivitySignals
	SpendCapRub   *float64
	CappedAt      *time.Time
	WarnedAt      *time.Time
	DeleteWarned  *time.Time
	EnvironmentID uuid.UUID
	Name          string
}

// effectiveCapRub is the box's cap, or the platform default when the creator named
// none. A default rather than "unlimited": the runaway this protects against is an
// agent in a loop, and an agent in a loop belongs to somebody who did not think
// about caps.
func (r boxMeterRow) effectiveCapRub(cfg *config.Config) float64 {
	if r.SpendCapRub != nil && *r.SpendCapRub > 0 {
		return *r.SpendCapRub
	}
	return cfg.BoxDefaultSpendCapRub
}

// MeterBoxMinutes meters the minute that has just completed, for every live box,
// then enforces the spend cap.
//
// It meters the PREVIOUS minute rather than the current one because a minute in
// progress has only been partly observed: billing it now and correcting it later
// would require rewriting a ledger row, and a ledger whose rows can be rewritten
// cannot settle an argument about a bill.
func (m *BoxMeter) MeterBoxMinutes(ctx context.Context) {
	now := m.now().UTC()
	minuteStart := now.Truncate(time.Minute).Add(-time.Minute)
	minuteEnd := minuteStart.Add(time.Minute)
	window := time.Duration(m.cfg.BoxActiveWindowSecs) * time.Second
	if window <= 0 {
		window = 2 * time.Minute
	}

	rows, err := m.loadBoxMeterRows(ctx, minuteEnd, window)
	if err != nil {
		metrics.RecordBoxMeterError()
		log.Warn().Err(err).Msg("box meter: failed to load boxes")
		return
	}

	activeByPlan := map[string]int{}
	idleByPlan := map[string]int{}

	for _, r := range rows {
		kind := classifyBoxMinute(r.Signals, minuteEnd, window, m.cfg.BoxActiveCPUPercent)
		if kind == "" {
			// THE IDLE PATH WRITES NOTHING. Counted in Prometheus so "idle is not
			// billed" is queryable, stored nowhere so it can never become billable.
			idleByPlan[r.Plan]++
			continue
		}
		if kind == boxUsageKindActive {
			activeByPlan[r.Plan]++
		}
		fp := m.pricer.footprintFor(r.Profile, kind)
		cost := m.pricer.perMinuteRub(fp)
		if err := m.writeMinute(ctx, r, minuteStart, kind, fp, cost); err != nil {
			metrics.RecordBoxMeterError()
			log.Warn().Err(err).Str("box", r.BoxID.String()).Str("kind", kind).
				Msg("box meter: failed to write usage row")
		}
	}

	for plan, n := range activeByPlan {
		metrics.RecordBoxMeteredMinutes(plan, n, 0)
	}
	for plan, n := range idleByPlan {
		metrics.RecordBoxMeteredMinutes(plan, 0, n)
	}

	// Lag is measured against the meter's OWN progress (how far behind the minute
	// it just finished is), not against MAX(minute_start) in the ledger. The
	// difference matters: a fleet that is entirely idle writes no rows at all, so a
	// ledger-derived lag would climb forever and page somebody about a meter that
	// is working perfectly. A minute the meter examined and found idle has been
	// metered; it simply produced no row.
	metrics.SetBoxMeteredMinutesLag(now.Sub(minuteEnd))

	m.enforceSpendCaps(ctx, rows, now)
}

// loadBoxMeterRows reads every box that could possibly be billable this minute,
// along with its plan and its activity signals.
//
// Deleted/Deleting/Failed boxes are excluded in SQL rather than by
// classifyBoxMinute so a large tombstone table costs nothing per tick; the
// classifier still refuses them, because a function that decides what a customer
// pays should not depend on its caller having filtered correctly.
func (m *BoxMeter) loadBoxMeterRows(ctx context.Context, minuteEnd time.Time, window time.Duration) ([]boxMeterRow, error) {
	// The operation-in-flight probe is bounded by updated_at: a worker advancing an
	// operation bumps that column, so a live operation keeps billing while an
	// operation abandoned in Created stops after the window. Without the bound a
	// single stuck row would bill its box forever, which is exactly the kind of
	// error that is discovered on an invoice.
	sql := `
		SELECT b.id, b.project_id, COALESCE(p.org_id, ''), b.profile, b.status,
		       COALESCE(ba.plan, 'free'),
		       b.last_sample_at, b.last_sample_active,
		       (b.last_sample_json ->> 'cpu_percent')::float8,
		       b.guest_heartbeat_at, b.last_active_at,
		       b.spend_cap_rub, b.spend_capped_at, b.spend_cap_warned_at,
		       b.spend_cap_delete_warned_at, b.environment_id, b.name,
		       EXISTS (
		         SELECT 1 FROM operations o
		          WHERE o.environment_id = b.environment_id
		            AND o.resource_kind = $1
		            AND o.status NOT IN ('Ready', 'Failed', 'Cancelled')
		            AND o.updated_at >= $2
		       )
		  FROM boxes b
		  JOIN projects p ON p.id = b.project_id
		  LEFT JOIN billing_accounts ba ON ba.org_id = p.org_id
		 WHERE b.status NOT IN ('Deleted', 'Deleting', 'Failed')`

	pgRows, err := m.pool.Query(ctx, sql, models.ResourceKindBox, minuteEnd.Add(-window))
	if err != nil {
		return nil, err
	}
	defer pgRows.Close()

	var out []boxMeterRow
	for pgRows.Next() {
		var r boxMeterRow
		var status string
		if err := pgRows.Scan(
			&r.BoxID, &r.ProjectID, &r.OrgID, &r.Profile, &status, &r.Plan,
			&r.Signals.AgentSampleAt, &r.Signals.AgentSampleActive, &r.Signals.AgentCPUPercent,
			&r.Signals.GuestHeartbeatAt, &r.Signals.TouchedAt,
			&r.SpendCapRub, &r.CappedAt, &r.WarnedAt, &r.DeleteWarned,
			&r.EnvironmentID, &r.Name, &r.Signals.OperationInFlight,
		); err != nil {
			return nil, err
		}
		r.Signals.Status = models.BoxStatus(status)
		r.Plan = m.knownPlanLabel(r.Plan)
		out = append(out, r)
	}
	return out, pgRows.Err()
}

// knownPlanLabel maps a billing_accounts.plan value onto the loaded plan catalog,
// returning "unknown" for anything that is not in it.
//
// This is a CARDINALITY GUARD, not tidiness. plan is a Prometheus label on
// dada_box_active_minutes_total, and billing_accounts.plan is a plain TEXT column
// with no foreign key: one bad row (a typo, a plan that was renamed, a manual
// UPDATE) would mint a permanent new series, and enough of them would take the
// scrape down. internal/metrics/box_surface_test.go bans per-org labels for the
// same reason; this is the same rule applied to a value we do not control.
func (m *BoxMeter) knownPlanLabel(plan string) string {
	for _, p := range m.plans {
		if p.Key == plan {
			return plan
		}
	}
	return "unknown"
}

// writeMinute inserts one ledger row.
//
// ON CONFLICT DO NOTHING, never DO UPDATE. The PK collision is the expected case
// on a replay or a second replica, and the FIRST verdict is the one that stands: a
// row that can be overwritten is a bill that can be silently restated, and the
// customer has no way to notice.
func (m *BoxMeter) writeMinute(ctx context.Context, r boxMeterRow, minuteStart time.Time, kind string, fp costengine.Footprint, cost float64) error {
	_, err := m.pool.Exec(ctx, `
		INSERT INTO box_usage (box_id, minute_start, kind, org_id, project_id,
		                       vcpu, ram_gb, storage_gb, cost_rub)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (box_id, minute_start, kind) DO NOTHING`,
		r.BoxID, minuteStart, kind, r.OrgID, r.ProjectID,
		fp.VCPU, fp.RAMGB, fp.StorageGB, cost,
	)
	return err
}

// boxSpend is one box's month-to-date accrual, split so the disk-only figure can
// be tested against boxSpendCapDiskDeleteMultiple without the compute spend
// masking it.
type boxSpend struct {
	Total float64
	Disk  float64
}

// enforceSpendCaps compares each box's month-to-date spend against its cap and
// acts.
//
// Every stamp below is written with the TICK's instant rather than with the
// database's now(). The webhook's rule ("the database clock, never the payload")
// is about not trusting a GUEST's clock and still holds; this is the backend's own
// clock, which is also the clock that decided which minute was being metered.
// Mixing the two would mean grace periods measured against one clock and metered
// minutes against another, and the arithmetic would only be right by coincidence —
// which is also why the grace period in enforceDiskAccrualLimit could not otherwise
// be tested at all.
//
// Three thresholds, three different actions, and the ordering between them is the
// product decision:
//
//	>= 80% of cap  -> ONE warning email. The customer can raise the cap or finish.
//	>= 100% of cap -> SUSPEND, never delete, and stamp spend_capped_at. The data
//	                  survives; the customer decides what happens to it. A runaway
//	                  must be able to cost somebody money and never their work.
//	disk >= 2x cap -> warn, then (after boxSpendCapDeleteGrace) delete. The only
//	                  destructive branch, and it needs one because a suspended box
//	                  keeps consuming storage indefinitely.
func (m *BoxMeter) enforceSpendCaps(ctx context.Context, rows []boxMeterRow, now time.Time) {
	if len(rows) == 0 {
		metrics.SetBoxSpendCapMaxRatio(0)
		return
	}
	spend, err := m.monthToDateSpend(ctx, now)
	if err != nil {
		metrics.RecordBoxMeterError()
		log.Warn().Err(err).Msg("box meter: failed to sum month-to-date spend")
		return
	}

	maxRatio := 0.0
	for _, r := range rows {
		s := spend[r.BoxID]
		capRub := r.effectiveCapRub(m.cfg)
		if capRub <= 0 {
			continue
		}
		if ratio := s.Total / capRub; ratio > maxRatio {
			maxRatio = ratio
		}

		switch {
		case s.Disk >= capRub*boxSpendCapDiskDeleteMultiple:
			m.enforceDiskAccrualLimit(ctx, r, s, capRub, now)
		case s.Total >= capRub:
			m.stopOnSpendCap(ctx, r, s, capRub, now)
		case s.Total >= capRub*boxSpendCapWarnRatio:
			m.warnOnSpendCap(ctx, r, s, capRub, now)
		default:
			// Below the cap again, which can only mean the cap was RAISED (the
			// ledger never shrinks). Raising spend_cap_rub is the deliberate act
			// that clears the stop — that is what "irreversible without an
			// operator action" means concretely, and clearing it here rather than
			// in ResumeBox is what stops a plain resume from being an accidental
			// unlock: a resume with the old cap is capped again on the next tick.
			m.clearSpendCap(ctx, r)
		}
	}
	metrics.SetBoxSpendCapMaxRatio(maxRatio)
}

// monthToDateSpend sums the ledger for the current calendar month, per box.
func (m *BoxMeter) monthToDateSpend(ctx context.Context, now time.Time) (map[uuid.UUID]boxSpend, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT box_id,
		       SUM(cost_rub),
		       COALESCE(SUM(cost_rub) FILTER (WHERE kind = $2), 0)
		  FROM box_usage
		 WHERE minute_start >= $1
		 GROUP BY box_id`,
		monthStart(now), boxUsageKindSuspendedDisk,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]boxSpend{}
	for rows.Next() {
		var id uuid.UUID
		var s boxSpend
		if err := rows.Scan(&id, &s.Total, &s.Disk); err != nil {
			return nil, err
		}
		out[id] = s
	}
	return out, rows.Err()
}

// warnOnSpendCap sends the 80% heads-up, at most once per box.
//
// The conditional UPDATE is the concurrency guard: with the meter running
// unguarded on every replica, whichever replica's UPDATE reports a row is the one
// that mails. A boolean check followed by an unconditional write would send one
// email per replica.
func (m *BoxMeter) warnOnSpendCap(ctx context.Context, r boxMeterRow, s boxSpend, capRub float64, now time.Time) {
	if r.WarnedAt != nil {
		return
	}
	tag, err := m.pool.Exec(ctx,
		`UPDATE boxes SET spend_cap_warned_at = $2, updated_at = now()
		  WHERE id = $1 AND spend_cap_warned_at IS NULL`, r.BoxID, now)
	if err != nil || tag.RowsAffected() == 0 {
		return
	}
	metrics.RecordBoxSpendCapHit("warned")
	subject, body := notify.ComposeBoxSpendCapWarning(r.Name, s.Total, capRub)
	m.mail(ctx, r, subject, body)
}

// stopOnSpendCap suspends the box and stamps spend_capped_at.
//
// SUSPEND, NEVER DELETE. The disk survives, the attached databases and buckets
// live outside the box and are untouched, and the customer decides what happens
// next. The stamp is what makes the stop irreversible without a deliberate act:
// nothing in the normal lifecycle clears spend_capped_at, so a plain ResumeBox
// would be capped again on the next tick. Raising spend_cap_rub is the only thing
// that clears it, and clearSpendCap is where that happens.
func (m *BoxMeter) stopOnSpendCap(ctx context.Context, r boxMeterRow, s boxSpend, capRub float64, now time.Time) {
	if r.CappedAt != nil {
		return
	}
	tag, err := m.pool.Exec(ctx,
		`UPDATE boxes SET spend_capped_at = $2, updated_at = now()
		  WHERE id = $1 AND spend_capped_at IS NULL`, r.BoxID, now)
	if err != nil || tag.RowsAffected() == 0 {
		return
	}
	metrics.RecordBoxSpendCapHit("stopped")
	if _, err := enqueueBoxReaperOperation(ctx, m.pool, r.ProjectID, r.EnvironmentID, r.Name,
		models.ActionSuspendBox, "spend_cap", models.SuspendBoxPayload{BoxID: r.BoxID, Reason: "spend_cap"}); err != nil {
		if errors.Is(err, errBoxOperationAlreadyPending) {
			log.Info().Str("box", r.BoxID.String()).Msg("box meter: spend-cap suspend already pending, not queuing another")
		} else {
			log.Warn().Err(err).Str("box", r.BoxID.String()).Msg("box meter: failed to enqueue spend-cap suspend")
		}
	}
	subject, body := notify.ComposeBoxSpendCapStopped(r.Name, s.Total, capRub)
	m.mail(ctx, r, subject, body)
}

// clearSpendCap lifts a stop whose cap has since been raised, and resets the
// warning stamp so the next approach warns again. It touches nothing when the box
// was not capped, so the common path is a no-op with no write.
func (m *BoxMeter) clearSpendCap(ctx context.Context, r boxMeterRow) {
	if r.CappedAt == nil && r.WarnedAt == nil {
		return
	}
	if _, err := m.pool.Exec(ctx,
		`UPDATE boxes
		    SET spend_capped_at = NULL, spend_cap_warned_at = NULL, updated_at = now()
		  WHERE id = $1 AND (spend_capped_at IS NOT NULL OR spend_cap_warned_at IS NOT NULL)`,
		r.BoxID); err != nil {
		log.Warn().Err(err).Str("box", r.BoxID.String()).Msg("box meter: failed to clear spend cap stamp")
	}
}

// enforceDiskAccrualLimit is the one destructive branch in the meter: a box whose
// SLEEPING disk alone has accrued twice its cap is warned once and destroyed a day
// later if nothing changes.
//
// A skipped enqueue (errBoxOperationAlreadyPending) returns before
// RecordBoxDestroy and the status flip to Deleting, for the same reason
// box_reaper.go's enqueueDelete does: a delete for this box is already in
// flight, so counting it again would double the destroy metric for one teardown.
func (m *BoxMeter) enforceDiskAccrualLimit(ctx context.Context, r boxMeterRow, s boxSpend, capRub float64, now time.Time) {
	if r.DeleteWarned == nil {
		tag, err := m.pool.Exec(ctx,
			`UPDATE boxes SET spend_cap_delete_warned_at = $2, updated_at = now()
			  WHERE id = $1 AND spend_cap_delete_warned_at IS NULL`, r.BoxID, now)
		if err != nil || tag.RowsAffected() == 0 {
			return
		}
		metrics.RecordBoxSpendCapHit("warned")
		subject, body := notify.ComposeBoxDiskAccrualWarning(r.Name, s.Disk, capRub, boxSpendCapDeleteGrace)
		m.mail(ctx, r, subject, body)
		return
	}
	if now.Before(r.DeleteWarned.Add(boxSpendCapDeleteGrace)) {
		return
	}
	if _, err := enqueueBoxReaperOperation(ctx, m.pool, r.ProjectID, r.EnvironmentID, r.Name,
		models.ActionDeleteBox, "spend_cap", models.DeleteBoxPayload{BoxID: r.BoxID, Reason: "spend_cap"}); err != nil {
		if errors.Is(err, errBoxOperationAlreadyPending) {
			log.Info().Str("box", r.BoxID.String()).Msg("box meter: disk-accrual delete already pending, not queuing another")
		} else {
			log.Warn().Err(err).Str("box", r.BoxID.String()).Msg("box meter: failed to enqueue disk-accrual delete")
		}
		return
	}
	metrics.RecordBoxSpendCapHit("stopped")
	metrics.RecordBoxDestroy("spend_cap")
	if _, err := m.pool.Exec(ctx,
		`UPDATE boxes SET status = 'Deleting', updated_at = now() WHERE id = $1 AND status <> 'Deleted'`,
		r.BoxID); err != nil {
		log.Warn().Err(err).Str("box", r.BoxID.String()).Msg("box meter: failed to mark box deleting")
	}
}

// mail sends one box notification to the box's owner, falling back to the operator
// address when no owner resolves — the same anti-drop rule the app watchers follow
// (resolveAlertRecipient). A nil/unconfigured notifier is a no-op, so local dev and
// tests never send anything.
func (m *BoxMeter) mail(ctx context.Context, r boxMeterRow, subject, body string) {
	if m.notifier == nil {
		return
	}
	to, _ := alertRecipientForProject(ctx, m.pool, r.ProjectID)
	if to == "" {
		to = m.operatorEmail
	}
	if to == "" {
		log.Warn().Str("box", r.BoxID.String()).Str("subject", subject).
			Msg("box notification has no recipient and no operator fallback; not sent")
		return
	}
	if err := m.notifier.Send(to, subject, body); err != nil {
		log.Warn().Err(err).Str("box", r.BoxID.String()).Msg("box notification send failed")
	}
}

// boxUsageMaxWindow caps how much ledger one request may scan. 92 days covers any
// "show me last quarter" question while bounding a single query to roughly 132k rows
// per box, which the (box_id, minute_start) index serves as a range scan.
const boxUsageMaxWindow = 92 * 24 * time.Hour

// parseBoxUsageWindow resolves ?from=&to= (RFC3339, or unix seconds) into a window.
//
// Defaults to the current calendar month, which is the window every other billing
// surface in this codebase reports on (monthStart, computeProjectConsumption), so a
// caller who passes nothing gets the number that matches their invoice preview
// rather than a different number that also happens to be true.
func parseBoxUsageWindow(fromS, toS string, now time.Time) (from, to time.Time, err error) {
	to = now
	from = monthStart(now)
	if toS != "" {
		if to, err = parseBoxUsageInstant(toS); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid to: %w", err)
		}
	}
	if fromS != "" {
		if from, err = parseBoxUsageInstant(fromS); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid from: %w", err)
		}
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("to must be after from")
	}
	if to.Sub(from) > boxUsageMaxWindow {
		return time.Time{}, time.Time{}, fmt.Errorf("window must not exceed %d days", int(boxUsageMaxWindow.Hours()/24))
	}
	return from.UTC(), to.UTC(), nil
}

// parseBoxUsageInstant accepts RFC3339 or unix seconds. Two formats because the
// console speaks RFC3339 and the metrics endpoints in this codebase already speak
// unix seconds; refusing one of them would make the box surface the odd one out.
func parseBoxUsageInstant(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	secs, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("expected RFC3339 or unix seconds, got %q", s)
	}
	return time.Unix(secs, 0).UTC(), nil
}

// GetBoxUsage returns one box's metered minutes and cost over a window.
//
// @ID          getBoxUsage
// @Summary     Get a box's metered minutes and cost
// @Description Returns the box's billed minutes and money over a window, read straight from the per-minute ledger. basis is always "actual": every row was written at the time the minute elapsed with its price frozen into it, so nothing here is modelled or estimated. Kinds are "active" (the box was doing your work) and "suspended_disk" (the box was asleep and only its disk was occupied). IDLE MINUTES ARE ABSENT ENTIRELY — an idle minute writes no row, which is why an idle box reports zero rather than a small charge. Window defaults to the current calendar month; ?from=&to= accept RFC3339 or unix seconds and may not span more than 92 days. Read-only, available to any project member.
// @Tags        box
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true  "Project UUID"
// @Param       boxName   path     string true  "Box name"
// @Param       from      query    string false "Window start (RFC3339 or unix seconds). Defaults to the first instant of the current calendar month."
// @Param       to        query    string false "Window end (RFC3339 or unix seconds). Defaults to now."
// @Success     200       {object} map[string]interface{} "period, currency, basis, totals and per-kind breakdown"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     500       {object} map[string]string
// @Router      /projects/{projectId}/boxes/{boxName}/usage [get]
func (h *Handler) GetBoxUsage(c *gin.Context) {
	_, projectID, ok := h.boxWriteGate(c, false)
	if !ok {
		return
	}
	b, ok := h.resolveBox(c, projectID, c.Param("boxName"))
	if !ok {
		return
	}
	from, to, err := parseBoxUsageWindow(c.Query("from"), c.Query("to"), h.clock())
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT kind, COUNT(*), COALESCE(SUM(cost_rub), 0)
		  FROM box_usage
		 WHERE box_id = $1 AND minute_start >= $2 AND minute_start < $3
		 GROUP BY kind
		 ORDER BY kind`,
		b.ID, from, to)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read box usage")
		return
	}
	defer rows.Close()

	kinds := []gin.H{}
	var totalRub float64
	var activeMinutes, billedMinutes int
	for rows.Next() {
		var kind string
		var minutes int
		var cost float64
		if err := rows.Scan(&kind, &minutes, &cost); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to read box usage")
			return
		}
		kinds = append(kinds, gin.H{"kind": kind, "minutes": minutes, "cost_rub": round2(cost)})
		totalRub += cost
		billedMinutes += minutes
		if kind == boxUsageKindActive {
			activeMinutes = minutes
		}
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read box usage")
		return
	}

	resp := gin.H{
		"period":         gin.H{"start": from.Format(time.RFC3339), "end": to.Format(time.RFC3339)},
		"currency":       "RUB",
		"basis":          basisActual,
		"active_minutes": activeMinutes,
		"billed_minutes": billedMinutes,
		"total_rub":      round2(totalRub),
		"kinds":          kinds,
	}
	// The cap is reported next to the spend so the customer never has to compute
	// "how close am I" themselves — that arithmetic is exactly what they would get
	// wrong at the moment it matters.
	if b.SpendCapRub != nil {
		resp["spend_cap_rub"] = *b.SpendCapRub
	}
	if b.SpendCappedAt != nil {
		// Present means the box was suspended by the cap and stays suspended until
		// the cap is raised. Suspended, never deleted: the disk and everything on it
		// survived.
		resp["spend_capped_at"] = b.SpendCappedAt
	}
	c.JSON(http.StatusOK, resp)
}

// boxSystemActorID is the fixed system-user id from migration 010_system_user.sql,
// used as actor_id when the PLATFORM acts on a box rather than a person: a spend cap
// firing, an idle sweep, a 72h reap.
//
// operations.actor_id is NOT NULL with a foreign key to users, so "no actor" is not
// expressible in this schema — and that is the right constraint, not an obstacle. An
// operation nobody is accountable for would be an operation nobody can be asked
// about. The convention already exists for exactly this case (see
// systemDeployActorID and reissueActorID), so the box loops reuse it rather than
// invent a second one; the payload's Reason field is what distinguishes "the
// platform suspended this because of the spend cap" from "a person clicked suspend".
var boxSystemActorID = uuid.MustParse("00000000-0000-0000-0000-000000000000")

// errBoxOperationAlreadyPending is returned by enqueueBoxReaperOperation when the
// box already has a non-terminal operation of the same action and the insert was
// skipped. It is not an error in the failure sense — the platform decision was
// already made and is still in flight — so callers must not Warn-log it or treat
// it as the enqueue failing; they log it at Info and otherwise proceed exactly as
// if their own insert had won.
var errBoxOperationAlreadyPending = errors.New("box operation already pending")

// enqueueBoxReaperOperation inserts one platform-initiated box operation and
// records it in the audit trail.
//
// A package function rather than a Handler method because the meter and the reaper
// are background loops with no request, no claims and no Handler.
//
// The audit row is written HERE rather than at the four call sites because this is
// the only door platform-initiated box work goes through, and forgetting it is
// exactly what happened: every user-facing box verb links its operation, while the
// reaper and the meter wrote nothing at all -- 271 of 274 SuspendBox operations in
// the last 30 days had no audit row, so the box pool ate boxes with no record of
// who decided to [live psql 2026-08-08]. A caller added later cannot repeat that
// omission without deleting code.
//
// The reason is a separate argument even though the payload already carries one:
// the payload shape differs per action, and a trail that has to unmarshal three
// structs to answer "why was this box killed" is a trail nobody queries.
//
// DEDUP IS IN THIS FUNCTION, not in the four call sites, for the same reason the
// audit write is: this is the one door, and a caller added later inherits the
// guard instead of having to remember it. ephprobe1's crash loop on 2026-08-11 is
// why it exists — SuspendBox kept failing mid-archive (a separate minio-go bug),
// boxes.status never reached Sleeping, and reapExpired/reapIdle called this
// function again on every 60s tick with nothing stopping a second, third, Nth
// SuspendBox row for the same box from queuing up behind the first. The insert and
// the "does one already exist" check are ONE statement (INSERT...SELECT...WHERE
// NOT EXISTS), not a SELECT followed by an INSERT: two replicas run this
// unguarded (box_meter.go's two call sites take no advisory lock) and briefly
// during a rolling deploy the reaper's own lock does not stop old and new pods
// from both holding it across a restart, so a check-then-act pair would still
// race. "Already exists" is read straight off terminalOperationStatuses
// (admin_overview.go) rather than a second literal list of statuses: that list is
// pinned to classifyOperationStatus by a dedicated test in admin_overview_
// terminality_test.go, and this repo has already shipped one panel that
// hand-rolled its own terminal set and drifted (Committed miscounted as
// stuck). WaitingForApproval is
// not in terminalOperationStatuses, so it counts as "already pending" here and
// blocks a second insert — box actions never route through approval today, but if
// one ever does, an operation parked on a human's decision is exactly the kind of
// in-flight work a second automatic insert must not duplicate.
//
// The key is (project_id, environment_id, action, resource_name): idx_boxes_
// project_name_live (migration 061) makes box names unique per project among
// live rows, so this key cannot conflate two different boxes or miss a repeat of
// the same one, and it uses columns the operations row already carries — no need
// to unmarshal payload to recover a box id.
func enqueueBoxReaperOperation(ctx context.Context, pool *pgxpool.Pool, projectID, environmentID uuid.UUID, boxName, action, reason string, payload any) (uuid.UUID, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil, err
	}
	var opID uuid.UUID
	err = pool.QueryRow(ctx,
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 SELECT $1, $2, $3, $4::varchar, $5::varchar, $6::varchar, 'Created', $7
		  WHERE NOT EXISTS (
		        SELECT 1 FROM operations
		         WHERE project_id = $2 AND environment_id = $3 AND action = $4
		           AND resource_kind = $5 AND resource_name = $6
		           AND status <> ALL($8::text[]))
		 RETURNING id`,
		boxSystemActorID, projectID, environmentID, action, models.ResourceKindBox, boxName, payloadBytes,
		terminalOperationStatuses).Scan(&opID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errBoxOperationAlreadyPending
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("enqueue %s for box %s: %w", action, boxName, err)
	}
	writeAuditRow(ctx, pool, boxSystemActorID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: environmentID,
		OperationID:   opID,
		Action:        action,
		ResourceKind:  models.ResourceKindBox,
		ResourceName:  boxName,
		Outcome:       auditOutcomeSuccess,
		Metadata:      map[string]any{"trigger": "platform", "reason": reason},
	})
	return opID, nil
}

package api

import (
	"context"
	"time"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/metrics"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// Dada Box lifecycle reaper: sleep on idle, sleep on TTL, destroy after 72h asleep.
//
// UNDER AN ADVISORY LOCK, unlike the meter next door, and the contrast is the
// point. box_meter.go writes rows keyed by a primary key, so running it on every
// replica is free. This loop ENQUEUES OPERATIONS AND SENDS MAIL — the exact
// lockKeyDomainReconcile case in advisory_lock.go's comment. Without the lock, three
// replicas would send three "your box will be deleted" emails per tick and enqueue
// three SuspendBox operations for one box, and the second and third would fail
// against a box that is already asleep. Idempotency here is possible but would have
// to be built out of unique stamps for every action; the lock is one line.

// lockKeyBoxReaper guards the box lifecycle reaper. Keys must stay distinct and
// stable across versions — a rolling deploy briefly runs old and new pods against
// the same database (see advisory_lock.go).
const lockKeyBoxReaper int64 = 0x64616461_0005

// boxSleepReapAfter is how long a box may sleep before it is destroyed. 72 hours,
// and it is a PRODUCT PROMISE rather than a tuning knob: the console says "мы
// храним спящий бокс 72 часа", so shortening it silently would break a printed
// statement. Two warning emails go out first.
const boxSleepReapAfter = 72 * time.Hour

// boxReapFirstWarnAfter / boxReapFinalWarnAfter are when those two emails go out,
// measured from the moment the box fell asleep. 48h leaves a full day to act; 66h
// is six hours before deletion, which is late enough to be alarming and early
// enough to be actionable.
const (
	boxReapFirstWarnAfter = 48 * time.Hour
	boxReapFinalWarnAfter = 66 * time.Hour
)

// boxReapGraceAfterFinalWarning is how long the final warning must have been out
// before the box may be destroyed.
//
// On the ordinary path it is redundant — 72h minus 66h is exactly this, so a box
// that slept normally reaches deletion six hours after its last email either way.
// It exists for the boxes that DID NOT arrive on the ordinary path: a row whose
// sleep clock was repaired by reapSleeping, or one that slept through an outage
// that stopped the mail, is already past 72h the first time this pass can see it.
// Without a floor here it would receive both warnings and its deletion inside two
// consecutive ticks — four minutes between "your box will be deleted" and the box
// being gone, which honours the letter of "warned twice" and none of its point.
const boxReapGraceAfterFinalWarning = boxSleepReapAfter - boxReapFinalWarnAfter

// staleCrystallizationGrace is the slack added on top of crystallizeBudget before a
// still-'Running' promotion is declared dead. A run that is one second from its own
// deadline is alive, and stealing its row would make two writers of one outcome.
const staleCrystallizationGrace = 2 * time.Minute

// BoxReaper is the lifecycle sweep. Like BoxMeter it carries an injected clock, so
// a test can put a box 71 hours asleep without waiting.
type BoxReaper struct {
	pool          *pgxpool.Pool
	cfg           *config.Config
	notifier      *notify.Notifier
	operatorEmail string
	now           func() time.Time
}

// NewBoxReaper builds the lifecycle sweep.
func NewBoxReaper(pool *pgxpool.Pool, cfg *config.Config, notifier *notify.Notifier) *BoxReaper {
	return &BoxReaper{
		pool:          pool,
		cfg:           cfg,
		notifier:      notifier,
		operatorEmail: cfg.AuditNotifyEmail,
		now:           func() time.Time { return time.Now().UTC() },
	}
}

// RunBoxMaintenanceTick runs one lifecycle pass under the box-reaper advisory lock,
// so with multiple backend replicas exactly one runs the pass per tick. Called from
// main's box ticker, beside the meter.
func (r *BoxReaper) RunBoxMaintenanceTick(ctx context.Context) {
	runWithAdvisoryLock(ctx, r.pool, lockKeyBoxReaper, "box-reaper", func(ctx context.Context) {
		r.reapStaleCrystallizations(ctx)
		r.reapIdle(ctx)
		r.reapExpired(ctx)
		r.reapSleeping(ctx)
	})
}

// boxReapCandidate is one box the sweep may act on.
type boxReapCandidate struct {
	BoxID         uuid.UUID
	ProjectID     uuid.UUID
	EnvironmentID uuid.UUID
	Name          string
	Status        models.BoxStatus
	IdleSeconds   int
	LastActiveAt  *time.Time
	SleptAt       *time.Time
	WarnedAt      *time.Time
	FinalWarnedAt *time.Time
}

// reapStaleCrystallizations closes out promotions whose process is gone.
//
// A crystallization is bounded by crystallizeBudget, and the handler that started
// it is the only thing that ever writes its ending. So a row still 'Running' long
// after that budget means the process that owned it no longer exists: the backend
// was rolled, the pod was evicted, the node died. Nothing will ever finish that row.
//
// The consequence is not cosmetic. The box stays 'Crystallizing', the partial
// unique index reads that row as "already in flight", and every retry is refused
// with 409 — the customer's box is permanently unpromotable because of an event
// that happened on our side and that they cannot see. That is the worst possible
// shape for the one action that converts a box into a paid VM.
//
// The grace on top of the budget is deliberate: a run at 14m59s is alive and must
// not have its row stolen out from under it while it is still writing files.
func (r *BoxReaper) reapStaleCrystallizations(ctx context.Context) {
	cutoff := r.now().UTC().Add(-(crystallizeBudget + staleCrystallizationGrace))
	rows, err := r.pool.Query(ctx, `
		UPDATE box_crystallizations
		   SET status = 'Failed', verified = false, finished_at = now(),
		       error_message = 'прервано перезапуском control-plane: процесс, который вёл кристаллизацию, не дожил до конца'
		 WHERE status = 'Running' AND created_at < $1
		RETURNING id, box_id`, cutoff)
	if err != nil {
		log.Warn().Err(err).Msg("box reaper: stale crystallization sweep failed")
		return
	}
	type stale struct{ crystID, boxID uuid.UUID }
	var found []stale
	for rows.Next() {
		var s stale
		if err := rows.Scan(&s.crystID, &s.boxID); err != nil {
			log.Warn().Err(err).Msg("box reaper: stale crystallization scan failed")
			rows.Close()
			return
		}
		found = append(found, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Warn().Err(err).Msg("box reaper: stale crystallization rows failed")
		return
	}
	for _, s := range found {
		if _, err := r.pool.Exec(ctx, `
			UPDATE boxes
			   SET status = 'Ready',
			       error_message = 'кристаллизация прервана перезапуском control-plane; можно повторить',
			       updated_at = now()
			 WHERE id = $1 AND status = 'Crystallizing'`, s.boxID); err != nil {
			log.Warn().Err(err).Str("box", s.boxID.String()).
				Msg("box reaper: failed to release a box from Crystallizing")
			continue
		}
		log.Warn().Str("box", s.boxID.String()).Str("crystallization", s.crystID.String()).
			Msg("box reaper: closed out a crystallization whose process is gone")
	}
}

// reapIdle puts awake-but-untouched boxes to sleep.
//
// The idle clock reads GREATEST(last_active_at, guest_heartbeat_at), which is where
// the in-guest heartbeat earns its keep: it can DEFER suspension. That is the
// permitted direction of the asymmetry documented in box_meter.go — an in-guest
// signal may ask to keep running (and therefore to be billed more), it may never
// claim idleness to be billed less. A guest that stops heartbeating simply loses
// its ability to defer; it does not gain the ability to look busy.
//
// A box with a LIVE EXPOSURE is not an idle candidate at all. Requests to a
// published hostname go ingress -> pod and never touch the control plane, so
// last_active_at does not move under real traffic: the idle clock cannot see the
// one kind of use that a published port exists for, and the box fell asleep under
// load while its URL was being served. Until the request counters of the edge are
// a signal this process can read, "the operator published a port" is the honest
// proxy for "this body is in use", and the hard TTL — not the idle clock — is what
// bounds it. That keeps the asymmetry intact: an exposure can only DEFER
// suspension, never claim idleness.
func (r *BoxReaper) reapIdle(ctx context.Context) {
	now := r.now().UTC()
	rows, err := r.pool.Query(ctx, `
		SELECT b.id, b.project_id, b.environment_id, b.name, b.status, b.idle_timeout_seconds
		  FROM boxes b
		 WHERE b.status IN ('Ready', 'Idle')
		   AND b.idle_timeout_seconds > 0
		   AND NOT EXISTS (
		         SELECT 1 FROM box_exposures e
		          WHERE e.box_id = b.id AND e.withdrawn_at IS NULL)
		   AND COALESCE(GREATEST(b.last_active_at, b.guest_heartbeat_at), b.created_at)
		       < $1::timestamptz - (b.idle_timeout_seconds * INTERVAL '1 second')`,
		now)
	if err != nil {
		log.Warn().Err(err).Msg("box reaper: idle query failed")
		return
	}
	candidates, err := scanBoxReapCandidates(rows)
	if err != nil {
		log.Warn().Err(err).Msg("box reaper: idle scan failed")
		return
	}
	for _, c := range candidates {
		r.enqueueSuspend(ctx, c, "idle")
	}
}

// reapExpired puts boxes past their hard TTL to sleep.
//
// SLEEP, NOT DELETE — and this is a deliberate divergence from one reading of the
// backlog, stated here rather than buried. Three shipped surfaces already promise
// it: models.Box's comment ("frozen after the idle timeout OR THE HARD TTL"),
// migration 061 ("8h hard TTL from claim" on a column next to idle_timeout_seconds),
// and — the binding one — the ExtendBox endpoint's published description in
// swagger.json: "Reaching the TTL puts a box to sleep, it never destroys it, so
// extending is a convenience rather than a rescue." Making the TTL destructive would
// turn that sentence into a false statement in a customer-facing API document, and
// the backlog's own standing rule is that a promise the architecture does not keep
// gets rewritten BEFORE it ships, not contradicted afterwards.
//
// Deletion therefore has exactly one automatic trigger: 72 hours asleep, after two
// emails (reapSleeping). The TTL feeds into that path — a box that expires at 8h
// starts its 72h sleep clock then — so nothing is retained forever; the difference
// is that no data is destroyed without the customer having been told twice.
func (r *BoxReaper) reapExpired(ctx context.Context) {
	now := r.now().UTC()
	rows, err := r.pool.Query(ctx, `
		SELECT b.id, b.project_id, b.environment_id, b.name, b.status, b.idle_timeout_seconds
		  FROM boxes b
		 WHERE b.status IN ('Ready', 'Idle')
		   AND b.expires_at IS NOT NULL
		   AND b.expires_at < $1`,
		now)
	if err != nil {
		log.Warn().Err(err).Msg("box reaper: ttl query failed")
		return
	}
	candidates, err := scanBoxReapCandidates(rows)
	if err != nil {
		log.Warn().Err(err).Msg("box reaper: ttl scan failed")
		return
	}
	for _, c := range candidates {
		r.enqueueSuspend(ctx, c, "ttl")
	}
}

// reapSleeping warns twice and then destroys a box that has been asleep for
// boxSleepReapAfter.
//
// The two warnings are stamped on the row (reap_warned_at, reap_final_warned_at)
// rather than counted in memory, so a pod restart cannot reset the count and a
// customer cannot be deleted having received one email or none.
func (r *BoxReaper) reapSleeping(ctx context.Context) {
	now := r.now().UTC()
	if tag, err := r.pool.Exec(ctx, `
		UPDATE boxes SET slept_at = updated_at
		 WHERE status = 'Sleeping' AND slept_at IS NULL`); err != nil {
		log.Warn().Err(err).Msg("box reaper: repairing sleeping boxes with no sleep clock failed")
	} else if tag.RowsAffected() > 0 {
		log.Warn().Int64("boxes", tag.RowsAffected()).
			Msg("box reaper: started the sleep clock on sleeping boxes that had none; until now nothing could ever reap them")
	}
	rows, err := r.pool.Query(ctx, `
		SELECT b.id, b.project_id, b.environment_id, b.name, b.status, b.idle_timeout_seconds,
		       b.slept_at, b.reap_warned_at, b.reap_final_warned_at
		  FROM boxes b
		 WHERE b.status = 'Sleeping' AND b.slept_at IS NOT NULL`)
	if err != nil {
		log.Warn().Err(err).Msg("box reaper: sleeping query failed")
		return
	}
	var candidates []boxReapCandidate
	for rows.Next() {
		var c boxReapCandidate
		var status string
		if err := rows.Scan(&c.BoxID, &c.ProjectID, &c.EnvironmentID, &c.Name, &status,
			&c.IdleSeconds, &c.SleptAt, &c.WarnedAt, &c.FinalWarnedAt); err != nil {
			log.Warn().Err(err).Msg("box reaper: sleeping scan failed")
			rows.Close()
			return
		}
		c.Status = models.BoxStatus(status)
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Warn().Err(err).Msg("box reaper: sleeping rows failed")
		return
	}

	for _, c := range candidates {
		asleep := now.Sub(*c.SleptAt)
		switch {
		case asleep >= boxSleepReapAfter:
			// Refuse to destroy a box that has not been warned twice. The clock alone
			// is not authority to delete somebody's work: if the mail path was broken
			// for the last three days, the honest outcome is a box that survives and a
			// warning in the log, not a silent deletion the customer learns about by
			// finding their prototype gone.
			//
			// The missing warning is sent one per tick, first then final, rather than
			// jumping to the final one. Sending only the final warning here was a trap
			// that could never spring: it stamps reap_final_warned_at and leaves
			// reap_warned_at NULL, so this branch is re-entered forever and the box is
			// never destroyed — a box past its 72 hours would have gone on holding its
			// volume for the rest of the platform's life while a log line claimed the
			// reaper was on the case.
			if c.WarnedAt == nil {
				log.Warn().Str("box", c.BoxID.String()).Dur("asleep", asleep).
					Msg("box reaper: past 72h asleep with no warning sent; sending the first one instead of deleting")
				r.sendReapWarning(ctx, c, asleep, false)
				continue
			}
			if c.FinalWarnedAt == nil {
				r.sendReapWarning(ctx, c, asleep, true)
				continue
			}
			if since := now.Sub(*c.FinalWarnedAt); since < boxReapGraceAfterFinalWarning {
				log.Info().Str("box", c.BoxID.String()).Dur("since_final_warning", since).
					Msg("box reaper: holding the delete until the final warning has had its grace")
				continue
			}
			r.enqueueDelete(ctx, c, "reaper")
		case asleep >= boxReapFinalWarnAfter && c.FinalWarnedAt == nil:
			r.sendReapWarning(ctx, c, asleep, true)
		case asleep >= boxReapFirstWarnAfter && c.WarnedAt == nil:
			r.sendReapWarning(ctx, c, asleep, false)
		}
	}
}

// enqueueSuspend moves an awake box toward sleep. The state transition is checked
// against models.CanTransitionBoxStatus first, so a box that raced into
// Crystallizing between the query and here is left alone rather than having a
// suspend queued behind a promotion.
func (r *BoxReaper) enqueueSuspend(ctx context.Context, c boxReapCandidate, reason string) {
	if !models.CanTransitionBoxStatus(c.Status, models.BoxStatusSleeping) {
		return
	}
	if err := enqueueBoxReaperOperation(ctx, r.pool, c.ProjectID, c.EnvironmentID, c.Name,
		models.ActionSuspendBox, models.SuspendBoxPayload{BoxID: c.BoxID, Reason: reason}); err != nil {
		log.Warn().Err(err).Str("box", c.BoxID.String()).Str("reason", reason).
			Msg("box reaper: failed to enqueue suspend")
		return
	}
	log.Info().Str("box", c.BoxID.String()).Str("reason", reason).Msg("box reaper: suspend queued")
}

// enqueueDelete destroys a box and marks it Deleting so a concurrent read never
// hands out a body that is being torn down (same ordering as the DeleteBox handler).
func (r *BoxReaper) enqueueDelete(ctx context.Context, c boxReapCandidate, reason string) {
	if err := enqueueBoxReaperOperation(ctx, r.pool, c.ProjectID, c.EnvironmentID, c.Name,
		models.ActionDeleteBox, models.DeleteBoxPayload{BoxID: c.BoxID, Reason: reason}); err != nil {
		log.Warn().Err(err).Str("box", c.BoxID.String()).Msg("box reaper: failed to enqueue delete")
		return
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE boxes SET status = 'Deleting', updated_at = now() WHERE id = $1 AND status = 'Sleeping'`,
		c.BoxID); err != nil {
		log.Warn().Err(err).Str("box", c.BoxID.String()).Msg("box reaper: failed to mark box deleting")
	}
	metrics.RecordBoxDestroy("ttl")
	log.Info().Str("box", c.BoxID.String()).Str("reason", reason).Msg("box reaper: delete queued")
}

// sendReapWarning sends one of the two pre-deletion emails and stamps the row.
//
// The stamp is written with a conditional UPDATE and only mails when it wins, for
// the same reason the spend cap does it that way: the advisory lock means one
// replica per tick, but a rolling deploy briefly runs two versions, and "one email"
// should not depend on the lock being held rather than on the row.
//
// THE REAP EMAIL IS THE CRYSTALLIZATION UPSELL MOMENT. See
// notify.ComposeBoxReapWarning: a customer who left a box asleep for two days has a
// prototype that survived, which is precisely the population the monetization ladder
// exists for, and this is the last moment we know they still care about its
// contents. An email that only announces a deletion throws that conversation away.
func (r *BoxReaper) sendReapWarning(ctx context.Context, c boxReapCandidate, asleep time.Duration, final bool) {
	column := "reap_warned_at"
	if final {
		column = "reap_final_warned_at"
	}
	// Stamped with the reaper's own clock, for the same reason the spend cap is (see
	// enforceSpendCaps): the sleep arithmetic is done against this clock, so the
	// stamps have to be on it too or the two warnings and the deletion are ordered by
	// coincidence.
	tag, err := r.pool.Exec(ctx,
		`UPDATE boxes SET `+column+` = $2, updated_at = now()
		  WHERE id = $1 AND `+column+` IS NULL`, c.BoxID, r.now().UTC())
	if err != nil || tag.RowsAffected() == 0 {
		return
	}
	deleteIn := boxSleepReapAfter - asleep
	if deleteIn < 0 {
		deleteIn = 0
	}
	subject, body := notify.ComposeBoxReapWarning(c.Name, asleep.Hours(), deleteIn.Hours(), final)
	r.mail(ctx, c.ProjectID, c.BoxID, subject, body)
}

// mail routes a reaper notification to the box's owner, falling back to the
// operator address so a warning is never silently dropped.
func (r *BoxReaper) mail(ctx context.Context, projectID, boxID uuid.UUID, subject, body string) {
	if r.notifier == nil {
		return
	}
	to, _ := alertRecipientForProject(ctx, r.pool, projectID)
	if to == "" {
		to = r.operatorEmail
	}
	if to == "" {
		log.Warn().Str("box", boxID.String()).Str("subject", subject).
			Msg("box reaper notification has no recipient and no operator fallback; not sent")
		return
	}
	if err := r.notifier.Send(to, subject, body); err != nil {
		log.Warn().Err(err).Str("box", boxID.String()).Msg("box reaper notification send failed")
	}
}

// scanBoxReapCandidates reads the six-column candidate shape shared by reapIdle and
// reapExpired.
func scanBoxReapCandidates(rows interface {
	Next() bool
	Scan(...any) error
	Close()
	Err() error
}) ([]boxReapCandidate, error) {
	defer rows.Close()
	var out []boxReapCandidate
	for rows.Next() {
		var c boxReapCandidate
		var status string
		if err := rows.Scan(&c.BoxID, &c.ProjectID, &c.EnvironmentID, &c.Name, &status, &c.IdleSeconds); err != nil {
			return nil, err
		}
		c.Status = models.BoxStatus(status)
		out = append(out, c)
	}
	return out, rows.Err()
}

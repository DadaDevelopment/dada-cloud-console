package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// dbQuotaWatchInterval is the poll period for the storage-quota watcher. A
// database grows far slower than an app crashes, and every tick that decides a
// state change costs a git commit, so this is deliberately slower than the
// volume watcher's 15m.
const dbQuotaWatchInterval = 30 * time.Minute

// The quota ladder: none -> warn(80%) -> read-only(100%), released at 90%.
// A single threshold would flap: a database sitting exactly at its limit would
// alternate between enforced and released on every tick, and each flip is a
// commit plus an ALTER ROLE. The release threshold is therefore well below the
// enforce threshold.
//
// read-only is the last rung on purpose. The XRD still accepts "frozen"
// (revoking CONNECT) for compatibility, but nothing here ever asks for it: a
// database whose owner cannot connect cannot be inspected, archived or emptied,
// so freezing removes the only ways out of the state it punishes.
const (
	dbQuotaWarnRatio    = 0.80
	dbQuotaEnforceRatio = 1.00
	dbQuotaReleaseRatio = 0.90
)

// dbQuotaGraceWindow is how long a database that is already over quota when a
// tier is first applied to it keeps full write access. Enforcement there is not
// a reaction to growth the owner watched happen -- the limit arrived under data
// that already existed -- so the owner gets a day, plus mail and a banner, to
// archive, delete or upgrade before the ladder resumes.
const dbQuotaGraceWindow = 24 * time.Hour

// dbQuotaGraceReminderLead is how long before the grace deadline the last
// letter goes out. Six hours is inside the same working day as the deadline for
// most of the estate, which is what makes it a reminder rather than a second
// copy of the first letter.
const dbQuotaGraceReminderLead = 6 * time.Hour

// dbQuotaWarnCooldown caps warning mail at one per database per window, the
// same anti-spam contract the volume watcher uses. State-change mail
// (enforced/released) is not covered by it: those are edges, not levels, so
// they cannot repeat on their own.
const dbQuotaWarnCooldown = 24 * time.Hour

// Enforcement states, mirroring ServiceDatabaseV2.spec.enforcement's XRD enum.
// A value outside this set is rejected by the API server at sync time, which
// would wedge the whole app's Application.
const (
	dbEnforcementNone     = "none"
	dbEnforcementReadOnly = "read-only"
	dbEnforcementFrozen   = "frozen"
)

// databaseTierLimitBytes is the storage quota per tier, mirroring
// serviceDatabase.tiers[].storageLimit in the crossplane-platform-api chart.
// It lives here rather than being read off the CR because PostgreSQL has no
// native per-database disk quota: the tier declares the limit, and this worker
// is the only thing that enforces it. 0 means no storage quota at all.
//
// TestDatabaseTierLimits_CoverEveryTier keeps this from drifting away from the
// chart's tier list.
var databaseTierLimitBytes = map[string]int64{
	"unlimited": 0,
	"internal":  0,
	"free":      1 << 30,
	"starter":   5 << 30,
	"business":  25 << 30,
}

// dbQuotaWatcher polls Prometheus for every managed database's on-disk size,
// compares it against its tier's quota, and drives the resulting enforcement
// state into git through SetDatabaseEnforcement operations. Decisions are
// persisted in db_quota_state so a restart does not re-enqueue an operation
// that has already been applied, and so the console can explain why a database
// refuses writes.
type dbQuotaWatcher struct {
	h *Handler
}

// StartDBQuotaWatcher launches the storage-quota watcher goroutine. No-op
// without Prometheus (nothing to measure), so local dev and tests never spawn
// it.
func (h *Handler) StartDBQuotaWatcher(ctx context.Context) {
	if h.prometheus == nil {
		return
	}
	w := &dbQuotaWatcher{h: h}
	log.Printf("db-quota: watcher started interval=%s warn=%.2f enforce=%.2f release=%.2f grace=%s",
		dbQuotaWatchInterval, dbQuotaWarnRatio, dbQuotaEnforceRatio, dbQuotaReleaseRatio, dbQuotaGraceWindow)
	go func() {
		runWithAdvisoryLock(ctx, h.pool, lockKeyDBQuotaWatch, "db-quota", w.tick)
		t := time.NewTicker(dbQuotaWatchInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runWithAdvisoryLock(ctx, h.pool, lockKeyDBQuotaWatch, "db-quota", w.tick)
			}
		}
	}()
}

// managedDatabase is one ServiceDatabaseV2 as the quota worker needs it: the
// identity to act on, the tier that sets the limit, the datname to measure by,
// and the enforcement state already recorded for it.
type managedDatabase struct {
	ProjectID     uuid.UUID
	EnvironmentID uuid.UUID
	Name          string
	Datname       string
	AppRef        string
	Tier          string
	Shard         string
	State         string
	GraceUntil    *time.Time
}

// dbSizesByDatnameFrom extracts datname → bytes out of raw pg_database_size_bytes
// samples, dropping non-finite values. Pure, so the decision path is testable
// without a live Prometheus.
func dbSizesByDatnameFrom(samples []prometheus.Sample) map[string]int64 {
	out := make(map[string]int64, len(samples))
	for _, s := range samples {
		dn := s.Metric["datname"]
		if dn == "" {
			continue
		}
		if math.IsNaN(s.Point.V) || math.IsInf(s.Point.V, 0) || s.Point.V < 0 {
			continue
		}
		out[dn] = int64(s.Point.V)
	}
	return out
}

// decideDBQuotaState maps a fill ratio and the current enforcement state onto
// the state that should hold now. It is the whole ladder, and it is pure: the
// hysteresis gap between dbQuotaEnforceRatio and dbQuotaReleaseRatio is what
// keeps a database parked at its limit from flapping between states (and
// therefore from producing a commit per tick).
//
// A database recorded as frozen by an older build falls through the default
// branch and is re-decided as read-only or none, which is the intended
// one-way downgrade: this worker never asks for frozen again.
func decideDBQuotaState(current string, ratio float64) string {
	if current == dbEnforcementReadOnly {
		if ratio < dbQuotaReleaseRatio {
			return dbEnforcementNone
		}
		return dbEnforcementReadOnly
	}
	if ratio >= dbQuotaEnforceRatio {
		return dbEnforcementReadOnly
	}
	return dbEnforcementNone
}

// applyDBQuotaGrace holds back the first enforcement of a database for
// dbQuotaGraceWindow. It returns the state to actually apply, whether a grace
// window should be opened now, and the grace deadline to persist (nil clears
// it).
//
// Grace is granted once per excursion, not once per tick: the deadline is
// stored, and it is only cleared when the database comes back under the
// release threshold. Without that, every tick would re-grant a fresh day and
// the ladder would never advance.
func applyDBQuotaGrace(want string, graceUntil *time.Time, now time.Time) (state string, startGrace bool, keep *time.Time) {
	if want != dbEnforcementReadOnly {
		return want, false, nil
	}
	if graceUntil == nil {
		until := now.Add(dbQuotaGraceWindow)
		return dbEnforcementNone, true, &until
	}
	if now.Before(*graceUntil) {
		return dbEnforcementNone, false, graceUntil
	}
	return dbEnforcementReadOnly, false, graceUntil
}

// tick measures every managed database once and applies the ladder. Every
// failure is logged and swallowed: one bad database must never block the rest,
// and the watcher must never crash the backend pod it runs inside.
func (w *dbQuotaWatcher) tick(ctx context.Context) {
	dbs, err := w.h.managedDatabasesForQuota(ctx)
	if err != nil {
		log.Printf("db-quota: load databases failed: %v", err)
		return
	}
	if len(dbs) == 0 {
		return
	}

	samples, err := w.h.prometheus.QueryInstant(ctx, "pg_database_size_bytes", time.Time{}, "")
	if err != nil {
		log.Printf("db-quota: size query failed: %v", err)
		return
	}
	sizes := dbSizesByDatnameFrom(samples)

	var measured, enforced int
	for _, d := range dbs {
		limit := databaseTierLimitBytes[d.Tier]
		if limit <= 0 {
			continue
		}
		size, ok := sizes[d.Datname]
		if !ok {
			continue
		}
		measured++
		ratio := float64(size) / float64(limit)
		ladder := decideDBQuotaState(d.State, ratio)
		want, startGrace, grace := applyDBQuotaGrace(ladder, d.GraceUntil, time.Now())
		w.recordQuotaState(ctx, d, limit, size, ratio, want, grace)
		if startGrace {
			w.notifyGraceStarted(ctx, d, size, limit, *grace)
			w.maybeAutoArchive(ctx, d, d.Shard, size, limit)
			continue
		}
		if ratio >= dbQuotaEnforceRatio {
			w.maybeAutoArchive(ctx, d, d.Shard, size, limit)
		}
		if grace != nil && want == dbEnforcementNone {
			w.maybeRemindGrace(ctx, d, size, limit, *grace)
		}
		if want != d.State {
			enforced++
			w.applyEnforcement(ctx, d, want, size, limit)
			continue
		}
		if want == dbEnforcementNone && ratio >= dbQuotaWarnRatio {
			w.maybeWarn(ctx, d, size, limit)
		}
	}
	log.Printf("db-quota: tick databases=%d measured=%d transitions=%d", len(dbs), measured, enforced)
}

// managedDatabasesForQuota lists every ServiceDatabaseV2 snapshot with the tier
// the reconciler observed live on the CR, joined to the enforcement state this
// worker last recorded. The live tier is used (not the plan) so a database
// whose CR has not been re-rendered yet is judged by the limit that is actually
// deployed for it.
func (h *Handler) managedDatabasesForQuota(ctx context.Context) ([]managedDatabase, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT rs.project_id, rs.environment_id, rs.name, rs.summary_json,
		        COALESCE(q.state, ''), q.grace_until
		 FROM resource_snapshots rs
		 LEFT JOIN db_quota_state q
		        ON q.environment_id = rs.environment_id AND q.name = rs.name
		 WHERE rs.kind = 'ServiceDatabaseV2'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []managedDatabase
	for rows.Next() {
		var d managedDatabase
		var summaryRaw []byte
		if err := rows.Scan(&d.ProjectID, &d.EnvironmentID, &d.Name, &summaryRaw, &d.State, &d.GraceUntil); err != nil {
			return nil, err
		}
		var summary struct {
			Tier string `json:"tier"`
			Spec struct {
				Database string `json:"database"`
				AppRef   string `json:"appRef"`
			} `json:"spec"`
		}
		if err := json.Unmarshal(summaryRaw, &summary); err != nil {
			continue
		}
		d.Datname = summary.Spec.Database
		d.AppRef = summary.Spec.AppRef
		d.Tier = summary.Tier
		d.Shard = serviceDatabaseShard(summaryRaw)
		if d.Tier == "" {
			d.Tier = "unlimited"
		}
		if d.State == "" {
			d.State = dbEnforcementNone
		}
		if d.Datname == "" {
			continue
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// recordQuotaState persists the measurement, the decided state and the grace
// deadline. state_since only moves when the state actually changes, so the
// console can say how long a database has been read-only rather than "since the
// last tick".
func (w *dbQuotaWatcher) recordQuotaState(ctx context.Context, d managedDatabase, limit, size int64, ratio float64, state string, graceUntil *time.Time) {
	_, err := w.h.pool.Exec(ctx,
		`INSERT INTO db_quota_state
		   (environment_id, name, project_id, tier, limit_bytes, size_bytes, ratio, state, grace_until, state_since, last_seen_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW(), NOW())
		 ON CONFLICT (environment_id, name) DO UPDATE SET
		   project_id = EXCLUDED.project_id,
		   tier = EXCLUDED.tier,
		   limit_bytes = EXCLUDED.limit_bytes,
		   size_bytes = EXCLUDED.size_bytes,
		   ratio = EXCLUDED.ratio,
		   state = EXCLUDED.state,
		   grace_until = EXCLUDED.grace_until,
		   grace_reminded_at = CASE WHEN db_quota_state.grace_until IS DISTINCT FROM EXCLUDED.grace_until
		                            THEN NULL ELSE db_quota_state.grace_reminded_at END,
		   state_since = CASE WHEN db_quota_state.state = EXCLUDED.state
		                      THEN db_quota_state.state_since ELSE NOW() END,
		   last_seen_at = NOW(),
		   updated_at = NOW()`,
		d.EnvironmentID, d.Name, d.ProjectID, d.Tier, limit, size, ratio, state, graceUntil)
	if err != nil {
		log.Printf("db-quota: state write for %s failed: %v", d.Name, err)
	}
}

// applyEnforcement enqueues the gitops operation that flips
// ServiceDatabaseV2.spec.enforcement, then audits and notifies. The operation
// is skipped when an unfinished one is already queued for the same database, so
// a slow gitops agent cannot cause a queue of contradictory flips.
func (w *dbQuotaWatcher) applyEnforcement(ctx context.Context, d managedDatabase, state string, size, limit int64) {
	opID, err := w.h.enqueueDatabaseEnforcement(ctx, d, state)
	if err != nil {
		log.Printf("db-quota: enqueue %s for %s failed: %v", state, d.Name, err)
		return
	}
	if opID == uuid.Nil {
		return
	}
	log.Printf("db-quota: %s -> %s (%.0f%% of quota) op=%s", d.Name, state, float64(size)/float64(limit)*100, opID)

	w.h.recordSystemAudit(ctx, auditEntry{
		ProjectID:     d.ProjectID,
		EnvironmentID: d.EnvironmentID,
		OperationID:   opID,
		Action:        "SetDatabaseEnforcement",
		ResourceKind:  "ServiceDatabaseV2",
		ResourceName:  d.Name,
		Metadata: map[string]any{
			"tier":        d.Tier,
			"from":        d.State,
			"to":          state,
			"size_bytes":  size,
			"limit_bytes": limit,
		},
	})
	w.notifyEnforcement(ctx, d, state, size, limit)
}

// enqueueDatabaseEnforcement inserts the operation, returning uuid.Nil when one
// is already in flight for this database. The dedupe is a single statement so
// two replicas racing on the same tick cannot both insert.
func (h *Handler) enqueueDatabaseEnforcement(ctx context.Context, d managedDatabase, state string) (uuid.UUID, error) {
	payload, err := json.Marshal(models.SetDatabaseEnforcementPayload{
		Name:        d.Name,
		AppRef:      d.AppRef,
		Enforcement: state,
	})
	if err != nil {
		return uuid.Nil, err
	}
	var opID uuid.UUID
	err = h.pool.QueryRow(ctx,
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 SELECT $1, $2, $3, 'SetDatabaseEnforcement', 'ServiceDatabaseV2', $4, 'Created', $5
		 WHERE NOT EXISTS (
		   SELECT 1 FROM operations
		   WHERE environment_id = $3 AND resource_kind = 'ServiceDatabaseV2' AND resource_name = $4
		     AND action = 'SetDatabaseEnforcement' AND status IN ('Created', 'Reconciling')
		 )
		 RETURNING id`,
		systemDeployActorID, d.ProjectID, d.EnvironmentID, d.Name, payload,
	).Scan(&opID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil
		}
		return uuid.Nil, err
	}
	return opID, nil
}

// notifyEnforcement mails the owner about a state edge. Recipient resolution
// mirrors the volume watcher's, including the operator fallback: an
// unreachable owner must not mean a database silently stops accepting writes.
func (w *dbQuotaWatcher) notifyEnforcement(ctx context.Context, d managedDatabase, state string, size, limit int64) {
	if w.h.auditNotifier == nil {
		return
	}
	to, source := w.h.resolveAlertRecipient(ctx, d.ProjectID)
	if to == "" {
		to = w.h.auditNotifyEmail
		source = alertSourceOperator
	}
	if to == "" {
		log.Printf("db-quota: no recipient for project %s, dropping %s notice for %s", d.ProjectID, state, d.Name)
		return
	}
	link := w.h.databasesConsoleLink(d.ProjectID)
	usedGB, limitGB := gigabytes(size), gigabytes(limit)
	var subject, body string
	if state == dbEnforcementNone {
		subject, body = notify.ComposeDatabaseQuotaReleased(d.Name, usedGB, limitGB, link)
	} else {
		subject, body = notify.ComposeDatabaseQuotaEnforced(d.Name, state, usedGB, limitGB, link)
	}
	if source == alertSourceOperator {
		subject, body = notify.ComposeNoOwnerFallback(d.ProjectID.String(), w.h.projectDisplayName(ctx, d.ProjectID), subject, body)
	}
	if err := w.h.auditNotifier.Send(to, subject, body); err != nil {
		log.Printf("db-quota: send to %s failed for %s: %v", to, d.Name, err)
		w.h.recordNotifySend(ctx, d.ProjectID, "DatabaseQuota", d.Name, source, err)
		return
	}
	w.h.recordNotifySend(ctx, d.ProjectID, "DatabaseQuota", d.Name, source, nil)
}

// notifyGraceStarted mails the owner when the grace window opens. It reuses the
// operator fallback of notifyEnforcement for the same reason: a database that
// will stop accepting writes tomorrow must not do so unannounced because the
// project has no reachable owner.
func (w *dbQuotaWatcher) notifyGraceStarted(ctx context.Context, d managedDatabase, size, limit int64, until time.Time) {
	log.Printf("db-quota: %s over quota at first sight (%.0f%%), grace until %s",
		d.Name, float64(size)/float64(limit)*100, until.UTC().Format(time.RFC3339))

	w.h.recordSystemAudit(ctx, auditEntry{
		ProjectID:     d.ProjectID,
		EnvironmentID: d.EnvironmentID,
		Action:        "DatabaseQuotaGrace",
		ResourceKind:  "ServiceDatabaseV2",
		ResourceName:  d.Name,
		Metadata: map[string]any{
			"tier":        d.Tier,
			"size_bytes":  size,
			"limit_bytes": limit,
			"grace_until": until.UTC().Format(time.RFC3339),
		},
	})

	if w.h.auditNotifier == nil {
		return
	}
	to, source := w.h.resolveAlertRecipient(ctx, d.ProjectID)
	if to == "" {
		to = w.h.auditNotifyEmail
		source = alertSourceOperator
	}
	if to == "" {
		log.Printf("db-quota: no recipient for project %s, dropping grace notice for %s", d.ProjectID, d.Name)
		return
	}
	subject, body := notify.ComposeDatabaseQuotaGrace(
		d.Name, d.Tier, gigabytes(size), gigabytes(limit), until.UTC(), w.h.databasesConsoleLink(d.ProjectID))
	if source == alertSourceOperator {
		subject, body = notify.ComposeNoOwnerFallback(d.ProjectID.String(), w.h.projectDisplayName(ctx, d.ProjectID), subject, body)
	}
	if err := w.h.auditNotifier.Send(to, subject, body); err != nil {
		log.Printf("db-quota: grace notice to %s failed for %s: %v", to, d.Name, err)
		w.h.recordNotifySend(ctx, d.ProjectID, "DatabaseQuotaGrace", d.Name, source, err)
		return
	}
	w.h.recordNotifySend(ctx, d.ProjectID, "DatabaseQuotaGrace", d.Name, source, nil)
}

// maybeRemindGrace sends the single letter that goes out shortly before a
// grace window closes.
//
// The claim is a conditional UPDATE rather than a decision made from the
// deadline: "under six hours left" stays true for twelve consecutive ticks, and
// an owner who receives the same warning twelve times stops reading any of
// them. A new grace window clears the claim, so a database that goes over quota
// again later is reminded again.
func (w *dbQuotaWatcher) maybeRemindGrace(ctx context.Context, d managedDatabase, size, limit int64, until time.Time) {
	left := time.Until(until)
	if left <= 0 || left > dbQuotaGraceReminderLead {
		return
	}
	if w.h.auditNotifier == nil {
		return
	}
	to, source := w.h.resolveAlertRecipient(ctx, d.ProjectID)
	if to == "" {
		to = w.h.auditNotifyEmail
		source = alertSourceOperator
	}
	if to == "" {
		return
	}
	if !claimDBQuotaGraceReminder(ctx, w.h.pool, d.EnvironmentID, d.Name) {
		return
	}
	subject, body := notify.ComposeDatabaseQuotaGraceEnding(
		d.Name, gigabytes(size), gigabytes(limit), until.UTC(),
		int(math.Ceil(left.Hours())), w.h.databasesConsoleLink(d.ProjectID))
	if source == alertSourceOperator {
		subject, body = notify.ComposeNoOwnerFallback(d.ProjectID.String(), w.h.projectDisplayName(ctx, d.ProjectID), subject, body)
	}
	if err := w.h.auditNotifier.Send(to, subject, body); err != nil {
		log.Printf("db-quota: grace reminder to %s failed for %s: %v", to, d.Name, err)
		w.h.recordNotifySend(ctx, d.ProjectID, "DatabaseQuotaGraceEnding", d.Name, source, err)
		return
	}
	w.h.recordNotifySend(ctx, d.ProjectID, "DatabaseQuotaGraceEnding", d.Name, source, nil)
}

// claimDBQuotaGraceReminder claims the one reminder this grace window is
// allowed, returning false when it has already been sent.
func claimDBQuotaGraceReminder(ctx context.Context, pool *pgxpool.Pool, envID uuid.UUID, name string) bool {
	tag, err := pool.Exec(ctx,
		`UPDATE db_quota_state SET grace_reminded_at = NOW()
		  WHERE environment_id = $1 AND name = $2 AND grace_reminded_at IS NULL`,
		envID, name)
	if err != nil {
		log.Printf("db-quota: grace reminder claim for %s failed: %v", name, err)
		return false
	}
	return tag.RowsAffected() == 1
}

// maybeWarn sends the pre-enforcement warning, gated by a 24h per-database
// cooldown claimed in db_quota_state (not in memory) so it holds across
// restarts and across replicas.
func (w *dbQuotaWatcher) maybeWarn(ctx context.Context, d managedDatabase, size, limit int64) {
	if w.h.auditNotifier == nil {
		return
	}
	to, source := w.h.resolveAlertRecipient(ctx, d.ProjectID)
	if to == "" {
		return
	}
	if !claimDBQuotaWarnSlot(ctx, w.h.pool, d.EnvironmentID, d.Name, dbQuotaWarnCooldown) {
		return
	}
	subject, body := notify.ComposeDatabaseQuotaWarning(
		d.Name, d.Tier, gigabytes(size), gigabytes(limit), w.h.databasesConsoleLink(d.ProjectID))
	if err := w.h.auditNotifier.Send(to, subject, body); err != nil {
		log.Printf("db-quota: warn to %s failed for %s: %v", to, d.Name, err)
		w.h.recordNotifySend(ctx, d.ProjectID, "DatabaseQuotaWarning", d.Name, source, err)
		return
	}
	w.h.recordNotifySend(ctx, d.ProjectID, "DatabaseQuotaWarning", d.Name, source, nil)
}

// claimDBQuotaWarnSlot atomically claims the right to send one warning for this
// database, succeeding only when no warning is recorded within cooldown. The
// row already exists by the time this runs (recordQuotaState writes it first on
// the same tick), so a plain conditional UPDATE is enough.
func claimDBQuotaWarnSlot(ctx context.Context, pool *pgxpool.Pool, envID uuid.UUID, name string, cooldown time.Duration) bool {
	ct, err := pool.Exec(ctx,
		`UPDATE db_quota_state SET last_warn_at = NOW()
		 WHERE environment_id = $1 AND name = $2
		   AND (last_warn_at IS NULL OR last_warn_at <= NOW() - make_interval(secs => $3))`,
		envID, name, cooldown.Seconds())
	if err != nil {
		log.Printf("db-quota: warn cooldown claim for %s failed: %v", name, err)
		return false
	}
	return ct.RowsAffected() > 0
}

// databasesConsoleLink deep-links to the project's Databases page, the one
// place where the quota state and the way out of it are both visible.
func (h *Handler) databasesConsoleLink(projectID uuid.UUID) string {
	return fmt.Sprintf("%s/projects/%s/databases", h.cfg.PublicBaseURL, projectID)
}

// gigabytes converts bytes to GiB for humans; quota mail is read by owners, not
// by machines.
func gigabytes(b int64) float64 {
	return float64(b) / float64(1<<30)
}

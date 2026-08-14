package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// dbTierReconcileInterval is the poll period for the tier reconciler. Tier
// drift follows plan changes, which are rare and never urgent: a database on
// the wrong tier keeps working, it is only measured against the wrong limit.
const dbTierReconcileInterval = 1 * time.Hour

// dbTierRetryAfter is how long a failed tier flip suppresses re-queuing the
// same tier for the same database. Long enough that a structurally impossible
// flip costs four operations a day instead of twenty-four, short enough that a
// transient git or agent failure heals the same working day.
const dbTierRetryAfter = 6 * time.Hour

// dbTierInternal is the tier reserved for platform-owned databases: no storage
// limit, so the quota watcher never measures them. The reconciler never
// overwrites it, and writes it for every database of a DB_QUOTA_EXEMPT_ORGS org.
//
// The write is the whole point. Not overwriting an "internal" tier protects
// nothing while nothing ever sets it: on 2026-08-14 all 21 managed databases sat
// at the XRD default "unlimited", and the reconciler's plan lookup was about to
// put cloud-console (1.4 GB), keycloak, nexus and powerdns on the 5 GB starter
// ceiling of org "dada" -- a read-only control plane and no SSO, one growth
// curve away.
const dbTierInternal = "internal"

// dbTierReconciler keeps every managed database's ServiceDatabaseV2.spec.tier
// equal to the tier its organization's billing plan implies.
//
// Without it the tier is only ever set at create time, so a database created on
// free and then upgraded to business keeps the 1 GB limit, and -- the case that
// actually mattered -- every database created before tiers existed sits at the
// XRD default "unlimited" and is invisible to the quota watcher. The watcher
// enforces limits; this is what gives it a limit to enforce.
type dbTierReconciler struct {
	h *Handler
}

// StartDBTierReconciler launches the tier reconciler goroutine.
func (h *Handler) StartDBTierReconciler(ctx context.Context) {
	r := &dbTierReconciler{h: h}
	log.Printf("db-tier: reconciler started interval=%s", dbTierReconcileInterval)
	go func() {
		runWithAdvisoryLock(ctx, h.pool, lockKeyDBTierReconcile, "db-tier", r.tick)
		t := time.NewTicker(dbTierReconcileInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runWithAdvisoryLock(ctx, h.pool, lockKeyDBTierReconcile, "db-tier", r.tick)
			}
		}
	}()
}

// tieredDatabase is one ServiceDatabaseV2 as the reconciler needs it: identity,
// the tier observed on the CR, and the organization whose plan decides the
// tier it should carry.
type tieredDatabase struct {
	ProjectID     uuid.UUID
	EnvironmentID uuid.UUID
	Name          string
	AppRef        string
	Tier          string
	OrgID         string
}

// tick brings every database's tier in line with its plan once. Failures are
// logged and swallowed per database: a project whose plan cannot be read must
// not stop the rest from being reconciled.
func (r *dbTierReconciler) tick(ctx context.Context) {
	dbs, err := r.h.tieredDatabases(ctx)
	if err != nil {
		log.Printf("db-tier: load databases failed: %v", err)
		return
	}

	planTier := make(map[string]string)
	var changed int
	for _, d := range dbs {
		if d.Tier == dbTierInternal || d.OrgID == "" {
			continue
		}
		if r.h.dbQuotaExemptOrg(d.OrgID) {
			r.retier(ctx, d, dbTierInternal, &changed)
			continue
		}
		want, ok := planTier[d.OrgID]
		if !ok {
			plan, err := r.h.planFor(ctx, d.OrgID)
			if err != nil {
				log.Printf("db-tier: plan lookup for org %s failed: %v", d.OrgID, err)
				continue
			}
			want = databaseTierByPlan[plan.Key]
			planTier[d.OrgID] = want
		}
		r.retier(ctx, d, want, &changed)
	}
	log.Printf("db-tier: tick databases=%d retiered=%d", len(dbs), changed)
}

// retier enqueues the tier flip unless the database already carries the tier or
// one is in flight, and records the audit entry the console explains it with.
func (r *dbTierReconciler) retier(ctx context.Context, d tieredDatabase, want string, changed *int) {
	if want == "" || want == d.Tier {
		return
	}
	opID, err := r.h.enqueueDatabaseTier(ctx, d, want)
	if err != nil {
		log.Printf("db-tier: enqueue %s for %s failed: %v", want, d.Name, err)
		return
	}
	if opID == uuid.Nil {
		return
	}
	*changed++
	log.Printf("db-tier: %s %s -> %s op=%s", d.Name, d.Tier, want, opID)
	r.h.recordSystemAudit(ctx, auditEntry{
		ProjectID:     d.ProjectID,
		EnvironmentID: d.EnvironmentID,
		OperationID:   opID,
		Action:        "SetDatabaseTier",
		ResourceKind:  "ServiceDatabaseV2",
		ResourceName:  d.Name,
		Metadata: map[string]any{
			"from": d.Tier,
			"to":   want,
		},
	})
}

// tieredDatabases lists every ServiceDatabaseV2 snapshot with the tier last
// observed on the CR and the org that owns the project. A snapshot with no
// org_id belongs to a project outside billing (the platform's own), and is
// skipped by the caller rather than filtered here so the log still counts it.
func (h *Handler) tieredDatabases(ctx context.Context) ([]tieredDatabase, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT rs.project_id, rs.environment_id, rs.name, rs.summary_json,
		        COALESCE(p.org_id::text, '')
		 FROM resource_snapshots rs
		 JOIN projects p ON p.id = rs.project_id
		 WHERE rs.kind = 'ServiceDatabaseV2'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []tieredDatabase
	for rows.Next() {
		var d tieredDatabase
		var summaryRaw []byte
		if err := rows.Scan(&d.ProjectID, &d.EnvironmentID, &d.Name, &summaryRaw, &d.OrgID); err != nil {
			return nil, err
		}
		var summary struct {
			Tier string `json:"tier"`
			Spec struct {
				AppRef string `json:"appRef"`
			} `json:"spec"`
		}
		if err := json.Unmarshal(summaryRaw, &summary); err != nil {
			continue
		}
		d.Tier = summary.Tier
		if d.Tier == "" {
			d.Tier = "unlimited"
		}
		d.AppRef = summary.Spec.AppRef
		out = append(out, d)
	}
	return out, rows.Err()
}

// enqueueDatabaseTier inserts the operation, returning uuid.Nil when one is
// already in flight for this database. The dedupe is a single statement so two
// replicas racing on the same tick cannot both insert, and so a slow gitops
// agent cannot accumulate a queue of tier flips.
//
// The same statement backs off after a failure: a tier the agent already
// rejected for this database is not re-queued for dbTierRetryAfter. A database
// whose CR is not in git at all cannot be tiered by any number of retries, and
// without the backoff the reconciler queued one operation per such database per
// hour forever - 17 of them on the first production tick - burying real
// failures in noise.
func (h *Handler) enqueueDatabaseTier(ctx context.Context, d tieredDatabase, tier string) (uuid.UUID, error) {
	payload, err := json.Marshal(models.SetDatabaseTierPayload{
		Name:   d.Name,
		AppRef: d.AppRef,
		Tier:   tier,
	})
	if err != nil {
		return uuid.Nil, err
	}
	var opID uuid.UUID
	err = h.pool.QueryRow(ctx,
		`INSERT INTO operations (actor_id, project_id, environment_id, action, resource_kind, resource_name, status, payload)
		 SELECT $1, $2, $3, 'SetDatabaseTier', 'ServiceDatabaseV2', $4::text, 'Created', $5::jsonb
		 WHERE NOT EXISTS (
		   SELECT 1 FROM operations
		   WHERE environment_id = $3 AND resource_kind = 'ServiceDatabaseV2' AND resource_name = $4::text
		     AND action = 'SetDatabaseTier' AND status IN ('Created', 'Reconciling')
		 )
		 AND NOT EXISTS (
		   SELECT 1 FROM operations
		   WHERE environment_id = $3 AND resource_kind = 'ServiceDatabaseV2' AND resource_name = $4::text
		     AND action = 'SetDatabaseTier' AND status = 'Failed'
		     AND payload->>'tier' = $6::text
		     AND created_at > now() - $7::interval
		 )
		 RETURNING id`,
		systemDeployActorID, d.ProjectID, d.EnvironmentID, d.Name, payload, tier,
		dbTierRetryAfter.String(),
	).Scan(&opID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil
		}
		return uuid.Nil, err
	}
	return opID, nil
}

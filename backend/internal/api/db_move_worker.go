package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Worker cadence. A move spends most of its life waiting for a copy that runs
// for hours, so polling often buys nothing; the one moment that matters, the
// cutover, is driven by the router's own timers rather than by this tick.
const dbMoveTickInterval = 15 * time.Second

// dbMoveNamespace is where the schema-copy Job runs. It is the namespace the
// shards live in, so the Job reaches them without crossing a NetworkPolicy that
// only admits the namespace's own pods.
const dbMoveNamespace = "databases"

// dbMoveSchemaImage carries pg_dump and psql. The console image cannot: it is
// alpine plus ca-certificates, and shipping Postgres client tools into the API
// image to run them a few times a year is the wrong trade.
const dbMoveSchemaImage = "bitnamilegacy/postgresql:17.6.0-debian-12-r4"

// schemaCopier makes the target database's schema match the source. An
// interface because the implementation is a Kubernetes Job, and the phase
// machine has to be testable without a cluster.
type schemaCopier interface {
	CopySchema(ctx context.Context, m dbMove, srcDSN, dstDSN string) (bool, error)
}

// dbMoveWorker drives every unfinished move one step per tick.
type dbMoveWorker struct {
	h      *Handler
	dsns   map[string]string
	router *routerAdmin
	copier schemaCopier
}

// StartDBMoveWorker launches the move driver. Without shard admin credentials
// or a reachable router admin there is nothing it could do safely, so it does
// not start: a move that cannot hold traffic is a move that drops writes.
func (h *Handler) StartDBMoveWorker(ctx context.Context) {
	if h.pool == nil || h.cfg == nil {
		return
	}
	dsns := parseShardAdminDSNs(h.cfg.DBShardAdminDSNs)
	if len(dsns) == 0 || h.cfg.DBRouterAdminHost == "" {
		return
	}
	w := &dbMoveWorker{
		h:    h,
		dsns: dsns,
		router: newRouterAdmin(h.cfg.DBRouterAdminHost, h.cfg.DBRouterAdminPort,
			h.cfg.DBRouterAdminUser, h.cfg.DBRouterAdminPassword),
		copier: newJobSchemaCopier(),
	}
	log.Printf("db-move: worker started interval=%s shards=%d", dbMoveTickInterval, len(dsns))
	go func() {
		t := time.NewTicker(dbMoveTickInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runWithAdvisoryLock(ctx, h.pool, lockKeyDBMoveDrive, "db-move", w.tick)
			}
		}
	}()
}

// tick advances every unfinished move. One failing move must not stall the
// others, so a step's error is recorded on its own row and the loop continues.
func (w *dbMoveWorker) tick(ctx context.Context) {
	rows, err := w.h.pool.Query(ctx, `
		SELECT id::text, datname, owner_role, source_shard, target_shard, phase, lag_bytes, updated_at
		FROM db_moves
		WHERE phase NOT IN ('done', 'failed')
		ORDER BY created_at
	`)
	if err != nil {
		log.Printf("db-move: read moves: %v", err)
		return
	}
	var moves []dbMove
	for rows.Next() {
		var m dbMove
		if err := rows.Scan(&m.ID, &m.Datname, &m.OwnerRole, &m.SourceShard,
			&m.TargetShard, &m.Phase, &m.LagBytes, &m.UpdatedAt); err != nil {
			log.Printf("db-move: scan move: %v", err)
			rows.Close()
			return
		}
		moves = append(moves, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("db-move: read moves: %v", err)
		return
	}

	for _, m := range moves {
		if err := w.step(ctx, m); err != nil {
			log.Printf("db-move: %s (%s -> %s) failed in phase %s: %v",
				m.Datname, m.SourceShard, m.TargetShard, m.Phase, err)
			w.fail(ctx, m, err)
		}
	}
}

// fail records why a move stopped. The move is left where its data is: nothing
// in the failure path touches routing, so a move that dies at any point leaves
// every client on the shard that still holds their writes.
func (w *dbMoveWorker) fail(ctx context.Context, m dbMove, cause error) {
	if _, err := w.h.pool.Exec(ctx,
		`UPDATE db_moves SET phase = 'failed', error = $2, updated_at = NOW() WHERE id = $1`,
		m.ID, cause.Error()); err != nil {
		log.Printf("db-move: record failure for %s: %v", m.Datname, err)
	}
}

func (w *dbMoveWorker) setPhase(ctx context.Context, m dbMove, phase string) error {
	_, err := w.h.pool.Exec(ctx,
		`UPDATE db_moves SET phase = $2, error = '', updated_at = NOW() WHERE id = $1`, m.ID, phase)
	return err
}

// connect opens an admin connection to one logical database on one shard.
func (w *dbMoveWorker) connect(ctx context.Context, shard, datname string) (*pgx.Conn, error) {
	dsn, ok := w.dsns[shard]
	if !ok {
		return nil, fmt.Errorf("no admin credentials for shard %q", shard)
	}
	cfg, err := configForDatabase(dsn, datname)
	if err != nil {
		return nil, err
	}
	return pgx.ConnectConfig(ctx, cfg)
}

// shardDSN points a shard's admin DSN at one database, for handing to psql or
// to CREATE SUBSCRIPTION.
func (w *dbMoveWorker) shardDSN(shard, datname string) (string, error) {
	dsn, ok := w.dsns[shard]
	if !ok {
		return "", fmt.Errorf("no admin credentials for shard %q", shard)
	}
	cfg, err := configForDatabase(dsn, datname)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, datname), nil
}

// step runs the one action the move's current phase calls for. Each phase ends
// by persisting the next one, so a console that dies between steps resumes
// where it was rather than repeating work that costs hours.
func (w *dbMoveWorker) step(ctx context.Context, m dbMove) error {
	switch m.Phase {
	case dbMovePending:
		return w.prepare(ctx, m)
	case dbMovePreparing, dbMoveSchema:
		return w.schema(ctx, m)
	case dbMoveSyncing:
		return w.sync(ctx, m)
	case dbMoveCutover:
		return w.cutover(ctx, m)
	default:
		return fmt.Errorf("unknown phase %q", m.Phase)
	}
}

// prepare gives the target shard the owner role and the empty database. Both
// are cluster-level facts that no amount of table replication carries.
func (w *dbMoveWorker) prepare(ctx context.Context, m dbMove) error {
	src, err := w.connect(ctx, m.SourceShard, "postgres")
	if err != nil {
		return fmt.Errorf("connect source: %w", err)
	}
	defer src.Close(ctx)
	dst, err := w.connect(ctx, m.TargetShard, "postgres")
	if err != nil {
		return fmt.Errorf("connect target: %w", err)
	}
	defer dst.Close(ctx)

	if err := copyRole(ctx, src, dst, m.OwnerRole); err != nil {
		return err
	}
	if err := ensureTargetDatabase(ctx, dst, m.Datname, m.OwnerRole); err != nil {
		return err
	}
	return w.setPhase(ctx, m, dbMoveSchema)
}

// schema waits for the dump-and-load Job, then opens the replication stream.
// Replication starts only once the schema is there: a subscription to a
// database with no tables copies nothing and reports itself perfectly in sync.
func (w *dbMoveWorker) schema(ctx context.Context, m dbMove) error {
	srcDSN, err := w.shardDSN(m.SourceShard, m.Datname)
	if err != nil {
		return err
	}
	dstDSN, err := w.shardDSN(m.TargetShard, m.Datname)
	if err != nil {
		return err
	}
	if w.copier == nil {
		return errors.New("schema copy is not configured")
	}
	done, err := w.copier.CopySchema(ctx, m, srcDSN, dstDSN)
	if err != nil {
		return err
	}
	if !done {
		return w.setPhase(ctx, m, dbMoveSchema)
	}

	srcDB, err := w.connect(ctx, m.SourceShard, m.Datname)
	if err != nil {
		return fmt.Errorf("connect source database: %w", err)
	}
	defer srcDB.Close(ctx)
	dstDB, err := w.connect(ctx, m.TargetShard, m.Datname)
	if err != nil {
		return fmt.Errorf("connect target database: %w", err)
	}
	defer dstDB.Close(ctx)

	if err := handOverObjects(ctx, dstDB, m.OwnerRole); err != nil {
		return err
	}
	if err := startReplication(ctx, srcDB, dstDB, m.Datname, srcDSN); err != nil {
		return err
	}
	return w.setPhase(ctx, m, dbMoveSyncing)
}

// sync watches the copy catch up and moves to cutover once it is close enough.
// The lag is stored on every tick because it is the only honest answer to "how
// long until my database moves".
//
// A stream that has not appeared yet is waited out rather than treated as a
// dead one, for dbMoveStreamGrace measured from the last tick that could read
// the lag at all - which is exactly the moment the subscription was created,
// because a tick that cannot read the lag writes nothing.
func (w *dbMoveWorker) sync(ctx context.Context, m dbMove) error {
	srcDB, err := w.connect(ctx, m.SourceShard, m.Datname)
	if err != nil {
		return fmt.Errorf("connect source database: %w", err)
	}
	defer srcDB.Close(ctx)

	dstDB, err := w.connect(ctx, m.TargetShard, m.Datname)
	if err != nil {
		return fmt.Errorf("connect target database: %w", err)
	}
	defer dstDB.Close(ctx)

	lag, err := replicationLag(ctx, srcDB, m.Datname)
	if errors.Is(err, errNoReplicationStream) && time.Since(m.UpdatedAt) < dbMoveStreamGrace {
		return nil
	}
	if err != nil {
		return err
	}
	if err := awaitInitialCopy(ctx, dstDB, m.Datname); err != nil {
		if errors.Is(err, errInitialCopyPending) {
			return nil
		}
		return err
	}
	if _, err := w.h.pool.Exec(ctx,
		`UPDATE db_moves SET lag_bytes = $2, updated_at = NOW() WHERE id = $1`, m.ID, lag); err != nil {
		return err
	}
	if lag > dbMoveCutoverLagBytes {
		return nil
	}
	return w.setPhase(ctx, m, dbMoveCutover)
}

// cutover is the second the customer feels.
//
// The router holds every client of this database, and only then does the rest
// happen: the last of the lag drains, sequence positions are copied, the move
// is marked as living on its new shard - which is what the rendered routing
// table reads - and the router is reloaded until every replica agrees. Clients
// are released afterwards, on the new shard, having waited rather than failed.
//
// Everything inside the held window is ordered so that a failure leaves the
// database where it was: nothing is dropped on the source, and the move only
// becomes 'done', which is what redirects traffic, after the data is provably
// complete on the target.
func (w *dbMoveWorker) cutover(ctx context.Context, m dbMove) error {
	targetHost, err := w.shardHost(ctx, m.TargetShard)
	if err != nil {
		return err
	}
	srcDB, err := w.connect(ctx, m.SourceShard, m.Datname)
	if err != nil {
		return fmt.Errorf("connect source database: %w", err)
	}
	defer srcDB.Close(ctx)
	dstDB, err := w.connect(ctx, m.TargetShard, m.Datname)
	if err != nil {
		return fmt.Errorf("connect target database: %w", err)
	}
	defer dstDB.Close(ctx)

	return w.router.Cutover(ctx, m.Datname, targetHost, func(ctx context.Context) error {
		if err := awaitInitialCopy(ctx, dstDB, m.Datname); err != nil {
			return err
		}
		lag, err := replicationLag(ctx, srcDB, m.Datname)
		if err != nil {
			return err
		}
		if lag > dbMoveCutoverLagBytes {
			return fmt.Errorf("lag grew to %d bytes while traffic was held", lag)
		}
		if err := copySequences(ctx, srcDB, dstDB); err != nil {
			return err
		}
		if err := finishReplication(ctx, srcDB, dstDB, m.Datname); err != nil {
			return err
		}
		if _, err := w.h.pool.Exec(ctx,
			`UPDATE db_moves SET phase = 'done', error = '', cutover_at = NOW(), updated_at = NOW()
			 WHERE id = $1`, m.ID); err != nil {
			return err
		}
		w.h.recordMovePlacement(ctx, m.Datname, m.TargetShard)
		return nil
	})
}

// shardHost reads where the target shard answers, which is the host the router
// must report before clients are released.
func (w *dbMoveWorker) shardHost(ctx context.Context, shard string) (string, error) {
	var host string
	if err := w.h.pool.QueryRow(ctx,
		`SELECT host FROM db_shards WHERE name = $1`, shard).Scan(&host); err != nil {
		return "", fmt.Errorf("read address of shard %q: %w", shard, err)
	}
	if host == "" {
		return "", fmt.Errorf("shard %q has no address", shard)
	}
	return host, nil
}

// jobSchemaCopier runs pg_dump | psql as a Kubernetes Job.
type jobSchemaCopier struct {
	cs kubernetes.Interface
}

func newJobSchemaCopier() schemaCopier {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil
	}
	return &jobSchemaCopier{cs: cs}
}

// dbMoveJobName is derived from the move id, so a retried tick finds the Job it
// already created instead of starting a second dump against the same database.
func dbMoveJobName(id string) string {
	short := strings.ReplaceAll(id, "-", "")
	if len(short) > 12 {
		short = short[:12]
	}
	return "db-move-" + short
}

// CopySchema creates the Job on first call and reports its outcome afterwards.
//
// The dump is schema-only and ownerless: every object is created by the admin
// the Job connects as, because a dump that carries owners fails on a shard
// where those roles do not exist yet. handOverObjects, run by the move once
// this Job succeeds, is what gives them back to the tenant.
func (c *jobSchemaCopier) CopySchema(ctx context.Context, m dbMove, srcDSN, dstDSN string) (bool, error) {
	name := dbMoveJobName(m.ID)
	job, err := c.cs.BatchV1().Jobs(dbMoveNamespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := c.cs.BatchV1().Jobs(dbMoveNamespace).Create(ctx,
			schemaCopyJob(name, m, srcDSN, dstDSN), metav1.CreateOptions{}); err != nil {
			return false, fmt.Errorf("create schema copy job: %w", err)
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read schema copy job: %w", err)
	}
	if job.Status.Succeeded > 0 {
		return true, nil
	}
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return false, fmt.Errorf("schema copy job %s failed: %s, see its pod logs", name, c.Reason)
		}
	}
	return false, nil
}

// schemaCopyJob builds the dump-and-load Job.
//
// ON_ERROR_STOP is on: a schema that loaded halfway would let replication start
// against a target missing tables, and those tables would be silently absent
// after the move rather than loudly missing before it.
//
// bash, not sh: /bin/sh in this image is dash, which has no pipefail, so a
// failing pg_dump would be reported by the exit status of psql - which happily
// succeeds on an empty stream.
func schemaCopyJob(name string, m dbMove, srcDSN, dstDSN string) *batchv1.Job {
	backoff := int32(2)
	ttl := int32(24 * 60 * 60)
	script := "set -euo pipefail\n" +
		"pg_dump --schema-only --no-owner --no-privileges \"$SRC\" | " +
		"psql -v ON_ERROR_STOP=1 \"$DST\"\n"
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: dbMoveNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":    "db-move",
				"dada.cloud/move-datname":   m.Datname,
				"dada.cloud/move-target":    m.TargetShard,
				"app.kubernetes.io/part-of": "dada-cloud",
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:    "copy-schema",
						Image:   dbMoveSchemaImage,
						Command: []string{"/bin/bash", "-c", script},
						Env: []corev1.EnvVar{
							{Name: "SRC", Value: srcDSN},
							{Name: "DST", Value: dstDSN},
						},
					}},
				},
			},
		},
	}
}

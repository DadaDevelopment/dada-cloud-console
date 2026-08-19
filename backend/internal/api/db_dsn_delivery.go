package api

import (
	"context"
	"errors"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/dada-tuda/console/backend/internal/crypto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// dbDSNDeliveryPollInterval is how often the post-create delivery goroutine
// re-checks the connection secret. Cheap (one resource_snapshots read plus one
// Secret Get), so a short interval costs little and gets the app connectable
// sooner after the composition finishes.
const dbDSNDeliveryPollInterval = 10 * time.Second

// dbDSNDeliveryMaxWait bounds how long the post-create goroutine keeps
// retrying before giving up and logging. Generous relative to how long a
// ServiceDatabaseV2 composition takes to publish its connection secret, so a
// normal provision always lands well inside it; past this point the
// GetDatabaseCredentials reveal path (attemptDatabaseDSNDelivery's sibling,
// seedDatabaseDSNIfAbsent) is the safety net, run synchronously whenever the
// owner opens the database page and reveals credentials.
const dbDSNDeliveryMaxWait = 30 * time.Minute

// auditActionSeedDatabaseDSN records the one write this file performs: DATABASE_URL
// landed in an app's env because of a managed database's own credentials
// secret, not because a person typed it. See databases.go:ErrDBCredentialsNotReady
// for why this cannot happen inline at creation time.
const auditActionSeedDatabaseDSN = "SeedDatabaseDSN"

// deliverDatabaseDSNAsync waits for a freshly ordered managed database's
// connection secret to exist and, once it does, injects DATABASE_URL into the
// app it is bound to -- the k8s/Crossplane counterpart of the VM track's
// synchronous seedEnvVar call in createManagedDatabase, which cannot run
// synchronously here because the secret does not exist until the composition
// finishes reconciling.
//
// Runs detached from the HTTP request (callers pass context.Background()):
// the caller has already returned 202 with the queued operation by the time
// this is worth checking. Stops on the first definitive outcome (seeded,
// already set by the user, or the database row is gone) and otherwise keeps
// polling until dbDSNDeliveryMaxWait, after which the reveal-credentials path
// is the only remaining way to finish the delivery.
func (h *Handler) deliverDatabaseDSNAsync(ctx context.Context, projectID, envID uuid.UUID, dbName, appRef string, actorID uuid.UUID) {
	deadline := time.Now().Add(dbDSNDeliveryMaxWait)
	ticker := time.NewTicker(dbDSNDeliveryPollInterval)
	defer ticker.Stop()
	for {
		done, err := h.attemptDatabaseDSNDelivery(ctx, projectID, envID, dbName, appRef, actorID, "auto")
		if done {
			return
		}
		if err != nil {
			log.Printf("db-dsn-delivery: %s/%s: %v", dbName, appRef, err)
			if errors.Is(err, crypto.ErrKeyMisconfigured) {
				log.Printf("db-dsn-delivery: %s/%s: GITOPS_ENCRYPTION_KEY is unusable in this process, so no retry can ever succeed; stopping after one attempt (fix the Secret and restart the backend)", dbName, appRef)
				return
			}
		}
		if time.Now().After(deadline) {
			log.Printf("db-dsn-delivery: gave up on %s/%s after %s; reveal-credentials remains available as a manual fallback", dbName, appRef, dbDSNDeliveryMaxWait)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// attemptDatabaseDSNDelivery makes one attempt to hand a managed database's
// DSN to its bound app's env. done=true means the caller should stop
// retrying: the database row is gone, the DSN was written, or the app already
// had one. done=false with a nil error means "not ready yet, try again
// later" (ErrDBCredentialsNotReady) and is not logged as a failure.
func (h *Handler) attemptDatabaseDSNDelivery(ctx context.Context, projectID, envID uuid.UUID, dbName, appRef string, actorID uuid.UUID, trigger string) (done bool, err error) {
	var summaryRaw []byte
	scanErr := h.pool.QueryRow(ctx,
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ServiceDatabaseV2' AND name = $3`,
		projectID, envID, dbName,
	).Scan(&summaryRaw)
	if scanErr == pgx.ErrNoRows {
		return true, nil
	}
	if scanErr != nil {
		return false, scanErr
	}

	namespace := serviceDatabaseNamespace(summaryRaw)
	creds, credErr := h.dbcreds.Resolve(ctx, namespace, appRef)
	if credErr != nil {
		if errors.Is(credErr, cloudtask.ErrDBCredentialsNotReady) {
			return false, nil
		}
		return false, credErr
	}

	datname := serviceDatabaseDatname(summaryRaw)
	host := creds.Endpoint
	if host == "" {
		host = dbName
	}
	port := creds.Port
	if port == "" {
		port = "5432"
	}
	dsn := postgresDSN(creds.Username, creds.Password, host, port, datname)
	if dsn == "" {
		return false, errors.New("resolved credentials but could not assemble a DSN (missing database name)")
	}

	seeded, seedErr := h.seedDatabaseDSNIfAbsent(ctx, projectID, envID, dbName, appRef, dsn, actorID, trigger)
	if seedErr != nil {
		return false, seedErr
	}
	_ = seeded
	return true, nil
}

// appEnvVarValue returns the decrypted value appName carries for key in envID,
// and whether a row exists at all. Callers that only need presence should use
// appEnvVarIsSet; callers deciding whether the platform may replace its own
// earlier value need to see what is actually stored.
func (h *Handler) appEnvVarValue(ctx context.Context, envID uuid.UUID, appName, key string) (string, bool, error) {
	var encrypted []byte
	err := h.pool.QueryRow(ctx,
		`SELECT value_encrypted FROM env_vars WHERE environment_id = $1 AND app_name = $2 AND key = $3`,
		envID, appName, key,
	).Scan(&encrypted)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	plain, err := crypto.DecryptToken(h.cfg.GitopsEncryptionKey, encrypted)
	if err != nil {
		return "", false, err
	}
	return string(plain), true, nil
}

// platformManagedDSNHost reports whether host is one the platform itself has
// ever issued for managed Postgres. The set is deliberately closed: the TLS
// front (db.pv.dada-tuda.ru), the in-cluster router it fronts, and the shard
// Services that predate the router. Anything else means the string came from
// somewhere other than us and is not ours to rewrite.
func platformManagedDSNHost(host string) bool {
	switch host {
	case pgRouterInternalHost, pgRouterTLSHostname:
		return true
	}
	return strings.HasPrefix(host, "pg-shard-") && strings.HasSuffix(host, ".databases.svc.cluster.local")
}

// supersedesPlatformIssuedDSN reports whether existing is a DSN the platform
// issued earlier for the very same database that fresh points at, and fresh now
// says something different.
//
// The 2026-08-15 measurement this exists for: after MANAGED_DB_TLS_DSN_ENABLED
// was turned on, the credentials page started showing db.pv.dada-tuda.ru with
// sslmode=require while both live user apps still carried the pre-flag string
// (pg-router.databases.svc.cluster.local, no sslmode at all). The console told
// the user one thing and their container connected with another, and a
// canonical `ssl: true` failed against a host that speaks no TLS with no lever
// to fix it.
//
// Identity is decided by credentials and database name, never by host: the host
// is the field allowed to move. If username, password or database name differ,
// or either host is outside the platform set, the stored value is treated as
// the user's and left untouched.
func supersedesPlatformIssuedDSN(existing, fresh string) bool {
	oldURL, err := url.Parse(strings.TrimSpace(existing))
	if err != nil {
		return false
	}
	newURL, err := url.Parse(strings.TrimSpace(fresh))
	if err != nil {
		return false
	}
	if !postgresDSNScheme(oldURL.Scheme) || !postgresDSNScheme(newURL.Scheme) {
		return false
	}
	if !platformManagedDSNHost(oldURL.Hostname()) || !platformManagedDSNHost(newURL.Hostname()) {
		return false
	}
	if oldURL.User == nil || newURL.User == nil {
		return false
	}
	if oldURL.User.Username() != newURL.User.Username() {
		return false
	}
	oldPass, oldHasPass := oldURL.User.Password()
	newPass, newHasPass := newURL.User.Password()
	if !oldHasPass || !newHasPass || oldPass != newPass {
		return false
	}
	if strings.TrimPrefix(oldURL.Path, "/") != strings.TrimPrefix(newURL.Path, "/") {
		return false
	}
	return strings.TrimSpace(existing) != strings.TrimSpace(fresh)
}

func postgresDSNScheme(scheme string) bool {
	return scheme == "postgresql" || scheme == "postgres"
}

// dsnHostForAudit pulls just the host out of a DSN so an audit row can record
// what moved without ever carrying the password.
func dsnHostForAudit(dsn string) string {
	u, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil {
		return "unparseable"
	}
	return u.Hostname()
}

// appEnvVarIsSet reports whether appName already carries a non-blank value for
// key in envID. Used to keep DSN delivery from ever overwriting a value the
// user set by hand, including one that points at a different database
// entirely -- the only thing worse than a missing DATABASE_URL is silently
// replacing a working one.
func (h *Handler) appEnvVarIsSet(ctx context.Context, envID uuid.UUID, appName, key string) (bool, error) {
	var encrypted []byte
	err := h.pool.QueryRow(ctx,
		`SELECT value_encrypted FROM env_vars WHERE environment_id = $1 AND app_name = $2 AND key = $3`,
		envID, appName, key,
	).Scan(&encrypted)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	plain, err := crypto.DecryptToken(h.cfg.GitopsEncryptionKey, encrypted)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(plain)) != "", nil
}

// seedDatabaseDSNIfAbsent is the one place both delivery paths (the async
// post-create poller and the synchronous reveal-credentials fallback) go
// through to actually write DATABASE_URL. Idempotent by construction: a
// second call for the same app/database, whether from a retried poll tick or
// from the user clicking reveal after the auto path already succeeded, finds
// the var already set and does nothing but record that it looked. Every call
// leaves an audit_events row -- intent (attempted to seed) plus verdict
// (seeded / refreshed_stale_platform_dsn / already_current / user_modified /
// failed) -- so "we wrote this DSN for the user" is
// provable after the fact.
func (h *Handler) seedDatabaseDSNIfAbsent(ctx context.Context, projectID, envID uuid.UUID, dbName, appRef, dsn string, actorID uuid.UUID, trigger string) (seeded bool, err error) {
	existing, found, err := h.appEnvVarValue(ctx, envID, appRef, "DATABASE_URL")
	if err != nil {
		return false, err
	}
	refreshing := false
	if found && strings.TrimSpace(existing) != "" {
		switch {
		case strings.TrimSpace(existing) == strings.TrimSpace(dsn):
			h.recordAudit(ctx, actorID, auditEntry{
				ProjectID: projectID, EnvironmentID: envID,
				Action: auditActionSeedDatabaseDSN, ResourceKind: "ServiceDatabaseV2", ResourceName: dbName,
				Outcome:  auditOutcomeSuccess,
				Metadata: map[string]any{"seeded": false, "reason": "already_current", "app_ref": appRef, "trigger": trigger},
			})
			return false, nil
		case supersedesPlatformIssuedDSN(existing, dsn):
			refreshing = true
		default:
			h.recordAudit(ctx, actorID, auditEntry{
				ProjectID: projectID, EnvironmentID: envID,
				Action: auditActionSeedDatabaseDSN, ResourceKind: "ServiceDatabaseV2", ResourceName: dbName,
				Outcome:  auditOutcomeSuccess,
				Metadata: map[string]any{"seeded": false, "reason": "user_modified", "app_ref": appRef, "trigger": trigger},
			})
			return false, nil
		}
	}
	if err := h.seedEnvVar(ctx, envID, appRef, "DATABASE_URL", dsn, actorID); err != nil {
		h.recordAudit(ctx, actorID, auditEntry{
			ProjectID: projectID, EnvironmentID: envID,
			Action: auditActionSeedDatabaseDSN, ResourceKind: "ServiceDatabaseV2", ResourceName: dbName,
			Outcome:  auditOutcomeFailure,
			Metadata: map[string]any{"app_ref": appRef, "trigger": trigger, "error": err.Error()},
		})
		return false, err
	}
	h.recordAudit(ctx, actorID, auditEntry{
		ProjectID: projectID, EnvironmentID: envID,
		Action: auditActionSeedDatabaseDSN, ResourceKind: "ServiceDatabaseV2", ResourceName: dbName,
		Outcome: auditOutcomeSuccess,
		Metadata: map[string]any{
			"seeded": true, "app_ref": appRef, "trigger": trigger,
			"reason":   dsnSeedReason(refreshing),
			"old_host": dsnHostForAudit(existing), "new_host": dsnHostForAudit(dsn),
		},
	})
	return true, nil
}

func dsnSeedReason(refreshing bool) string {
	if refreshing {
		return "refreshed_stale_platform_dsn"
	}
	return "seeded"
}

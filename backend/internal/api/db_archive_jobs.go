package api

import (
	"fmt"
	"strings"

	"github.com/dada-tuda/console/backend/internal/cloudtask"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// defaultDBArchiveExportImage carries the DuckDB CLI, which is what turns a
// Postgres range into Parquet on S3 in one statement. The console image cannot:
// it is alpine plus ca-certificates, and the export has to run next to the
// shards anyway. Overridable per installation because this is a third-party
// image that has to be mirrored into the registry the cluster is allowed to
// pull from.
const defaultDBArchiveExportImage = "datacatering/duckdb:v1.3.2"

// defaultDBArchiveRepackImage carries the psql that runs the rewrite. Same
// reasoning, same override. It is a pg_repack image for historical reasons and
// because it ships a matching psql 17; the binary itself goes unused, since
// pg_repack needs a server extension the shards cannot install.
const defaultDBArchiveRepackImage = "hartmutcouk/pg-repack-docker:1.5.2"

// archiveRepackLockTimeout bounds how long the rewrite waits for the table's
// ACCESS EXCLUSIVE lock. Long enough to outlast an ordinary statement, short
// enough that a busy table fails the phase instead of parking every later query
// behind the pending lock request. It is set by its own -c because VACUUM FULL
// refuses to run inside the implicit transaction psql wraps a multi-statement
// -c in, and psql keeps one session across every -c.
const archiveRepackLockTimeout = "15s"

// archiveDuckDBResolve is the shell line every DuckDB phase starts with. The
// upstream image ships the CLI as /duckdb and puts nothing on PATH, so a script
// that calls "duckdb" dies with "command not found" -- which is exactly how the
// first real export failed, three pods deep into the Job backoff, with the run
// row blaming BackoffLimitExceeded. A mirrored image may well put the binary on
// PATH, so the lookup prefers PATH and falls back to the known location.
const archiveDuckDBResolve = "DUCKDB=$(command -v duckdb || echo /duckdb)"

// archiveJobTTLSeconds keeps a finished Job around long enough for an operator
// to read its logs after the run row already says what happened.
const archiveJobTTLSeconds = int32(24 * 60 * 60)

// archiveJobBackoff is how many times Kubernetes retries a phase before the run
// is failed. Every phase is idempotent -- the export overwrites, the verify
// only reads, the repack is a no-op on an already-repacked table -- so a retry
// is safe, but a phase that fails twice is failing for a reason a third attempt
// will not fix.
const archiveJobBackoff = int32(2)

// archivePGConn is a shard's admin connection broken into the parts libpq reads
// from the environment. The rewrite Job takes no connection string, so it is
// given PGHOST/PGPORT/PGUSER/PGPASSWORD instead.
type archivePGConn struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
}

// duckDBEndpoint splits an S3 endpoint into the host DuckDB wants and whether
// to speak TLS to it. DuckDB's s3_endpoint takes a bare host, and passing it a
// URL yields a request to a host literally named "https:". Pure.
func duckDBEndpoint(endpoint string) (string, bool) {
	host := strings.TrimSpace(endpoint)
	useSSL := true
	if rest, ok := strings.CutPrefix(host, "http://"); ok {
		host, useSSL = rest, false
	} else if rest, ok := strings.CutPrefix(host, "https://"); ok {
		host = rest
	}
	return strings.TrimSuffix(host, "/"), useSSL
}

// duckDBS3Settings is the preamble every DuckDB script needs to reach the
// archive bucket.
//
// The keys come from the environment through DuckDB's own credential chain
// rather than being written into the script: the script is world-readable
// inside the pod and is stored verbatim in the Job spec, and neither is a place
// for a bucket's secret key. Pure.
func duckDBS3Settings(creds cloudtask.S3Credentials) string {
	host, useSSL := duckDBEndpoint(creds.Endpoint)
	return fmt.Sprintf(`INSTALL httpfs; LOAD httpfs;
CREATE OR REPLACE SECRET dada_archive (
  TYPE S3, PROVIDER credential_chain, CHAIN 'env',
  ENDPOINT '%s', URL_STYLE 'path', USE_SSL %t
);`, host, useSSL)
}

// duckDBIdentifier quotes an identifier for a DuckDB script, doubling embedded
// quotes the way SQL requires. Pure.
func duckDBIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// archiveExportSQL is the whole export: attach the tenant database read-only,
// select everything older than the cutoff, write one Parquet object.
//
// The rows are the same ones the plan counted and the delete will remove, so
// the three steps cannot disagree about which rows the archive covers. Pure.
func archiveExportSQL(r archiveRun, creds cloudtask.S3Credentials) string {
	return fmt.Sprintf(`%s
INSTALL postgres; LOAD postgres;
ATTACH '' AS src (TYPE POSTGRES, READ_ONLY);
COPY (
  %s
) TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD);`,
		duckDBS3Settings(creds), archiveExportSelectSQL(r), r.S3URI)
}

// archiveVerifyScript reads the archive back and fails the Job unless it holds
// exactly the counted rows and nothing at or after the cutoff.
//
// The comparison is done in the shell rather than in SQL so that the check's
// verdict is the Job's exit status, which is the only signal the worker trusts
// before it deletes anything. Dates compare as strings because ISO-8601 sorts
// lexically, which is what makes "no row newer than the cutoff leaked in"
// expressible without parsing timestamps in bash. Pure.
func archiveVerifyScript(r archiveRun, creds cloudtask.S3Credentials) string {
	settings := duckDBS3Settings(creds)
	cutoff := r.Cutoff.Format("2006-01-02")
	cutoffColumn := archiveCutoffColumnInArchive(r)
	return fmt.Sprintf(`set -euo pipefail
cat > /tmp/count.sql <<'SQL'
%s
SELECT count(*) FROM read_parquet('%s');
SQL
cat > /tmp/newest.sql <<'SQL'
%s
SELECT coalesce(max(%s)::VARCHAR, '') FROM read_parquet('%s');
SQL
%s
ROWS=$("$DUCKDB" -noheader -list -f /tmp/count.sql | tail -n 1)
if [ "$ROWS" != "%d" ]; then
  echo "archive holds $ROWS rows, expected %d: refusing to delete"
  exit 1
fi
NEWEST=$("$DUCKDB" -noheader -list -f /tmp/newest.sql | tail -n 1)
if [ -z "$NEWEST" ]; then
  echo "archive has no value in %s: refusing to delete"
  exit 1
fi
if ! [ "$NEWEST" '<' "%s" ]; then
  echo "archive holds a row at $NEWEST, at or after the cutoff %s: refusing to delete"
  exit 1
fi
echo "verified $ROWS rows, newest $NEWEST, cutoff %s"
`,
		settings, r.S3URI,
		settings, duckDBIdentifier(cutoffColumn), r.S3URI,
		archiveDuckDBResolve,
		r.PlannedRows, r.PlannedRows,
		cutoffColumn,
		cutoff, cutoff, cutoff)
}

// archiveRepackScript rewrites the one table the run touched so the deleted
// rows' space goes back to the filesystem.
//
// It is VACUUM FULL and not pg_repack, which the phase is still named after,
// because pg_repack needs a server-side extension and the shards run the stock
// bitnami postgresql image: on shard-0 "select * from pg_available_extensions
// where name = 'pg_repack'" returns nothing, so CREATE EXTENSION could never
// succeed and the phase was undeliverable on every managed database we have.
//
// The price is the ACCESS EXCLUSIVE lock VACUUM FULL holds for the rewrite --
// measured at 2.9s for a 295 MB table, so roughly a minute per 5 GB. lock_timeout
// keeps that from turning into a queue: if the table is busy the phase fails
// with a lock error the run row can show, instead of stalling every query
// behind a lock request that waits forever.
//
// Space is reclaimed only when this runs. Plain VACUUM would mark the pages
// reusable and leave pg_database_size -- what the quota measures -- unchanged,
// which would make the whole archive pointless to the owner it is offered to.
// Pure.
func archiveRepackScript(r archiveRun) string {
	table := duckDBIdentifier(r.Schema) + "." + duckDBIdentifier(r.Table)
	return fmt.Sprintf(`set -euo pipefail
psql -v ON_ERROR_STOP=1 -c "SET lock_timeout = '%s'" -c %s
`, archiveRepackLockTimeout, shellSingleQuoted("VACUUM (FULL, VERBOSE) "+table))
}

// shellSingleQuoted wraps a string so the shell passes it through untouched,
// including the double quotes an SQL identifier needs. A table name is a
// tenant-chosen string: it reaches this script only through single quotes, and
// an embedded single quote is closed and re-opened rather than escaped, because
// backslash escapes do not apply inside shell single quotes. Pure.
func shellSingleQuoted(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// archiveJobSpec wraps a shell script in the Job shape every archive phase
// uses: run once, never restart the container, keep the logs for a day.
func archiveJobSpec(name, image, script string, env []corev1.EnvVar, r archiveRun) *batchv1.Job {
	backoff := archiveJobBackoff
	ttl := archiveJobTTLSeconds
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: dbArchiveNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":     "db-archive",
				"app.kubernetes.io/part-of":  "dada-cloud",
				"dada.cloud/archive-datname": r.Datname,
				"dada.cloud/archive-table":   r.Table,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:    "archive",
						Image:   image,
						Command: []string{"/bin/bash", "-c", script},
						Env:     env,
					}},
				},
			},
		},
	}
}

// archiveS3Env hands the bucket's keys to DuckDB's credential chain under the
// names it reads them by.
func archiveS3Env(creds cloudtask.S3Credentials) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: "AWS_ACCESS_KEY_ID", Value: creds.AccessKey},
		{Name: "AWS_SECRET_ACCESS_KEY", Value: creds.SecretKey},
		{Name: "AWS_DEFAULT_REGION", Value: "ru1"},
	}
}

// archivePGEnv hands a shard's admin connection to a container as the variables
// libpq reads. Both the DuckDB postgres extension (which attaches an empty DSN)
// and the rewrite (which takes no connection string at all) get their connection
// this way, so the password never reaches a command line or a script body.
func archivePGEnv(conn archivePGConn) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: "PGHOST", Value: conn.Host},
		{Name: "PGPORT", Value: fmt.Sprintf("%d", conn.Port)},
		{Name: "PGUSER", Value: conn.User},
		{Name: "PGPASSWORD", Value: conn.Password},
		{Name: "PGDATABASE", Value: conn.Database},
	}
}

// archiveExportJob writes the run's rows to Parquet on S3.
func archiveExportJob(name string, r archiveRun, creds cloudtask.S3Credentials, conn archivePGConn, image string) *batchv1.Job {
	script := fmt.Sprintf(`set -euo pipefail
cat > /tmp/export.sql <<'SQL'
%s
SQL
%s
"$DUCKDB" -f /tmp/export.sql
`, archiveExportSQL(r, creds), archiveDuckDBResolve)
	return archiveJobSpec(name, image, script, append(archiveS3Env(creds), archivePGEnv(conn)...), r)
}

// archiveVerifyJob reads the archive back and exits non-zero unless it is
// exactly what the plan counted.
func archiveVerifyJob(name string, r archiveRun, creds cloudtask.S3Credentials, image string) *batchv1.Job {
	return archiveJobSpec(name, image, archiveVerifyScript(r, creds), archiveS3Env(creds), r)
}

// archiveRepackJob returns the deleted rows' space to the filesystem.
func archiveRepackJob(name string, r archiveRun, conn archivePGConn, image string) *batchv1.Job {
	return archiveJobSpec(name, image, archiveRepackScript(r), archivePGEnv(conn), r)
}

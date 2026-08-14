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

// defaultDBArchiveRepackImage carries pg_repack plus the psql that installs its
// extension. Same reasoning, same override.
const defaultDBArchiveRepackImage = "hartmutcouk/pg-repack-docker:1.5.2"

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
// from the environment. pg_repack takes no connection string, so the Job is
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
// The predicate is the same one the plan counted and the delete will use, so
// the three steps cannot disagree about which rows the archive covers. Pure.
func archiveExportSQL(r archiveRun, creds cloudtask.S3Credentials) string {
	return fmt.Sprintf(`%s
INSTALL postgres; LOAD postgres;
ATTACH '' AS src (TYPE POSTGRES, READ_ONLY);
COPY (
  SELECT * FROM src.%s.%s WHERE %s < DATE '%s'
) TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD);`,
		duckDBS3Settings(creds),
		duckDBIdentifier(r.Schema), duckDBIdentifier(r.Table),
		duckDBIdentifier(r.CutoffColumn), r.Cutoff.Format("2006-01-02"), r.S3URI)
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
	return fmt.Sprintf(`set -euo pipefail
cat > /tmp/count.sql <<'SQL'
%s
SELECT count(*) FROM read_parquet('%s');
SQL
cat > /tmp/newest.sql <<'SQL'
%s
SELECT coalesce(max(%s)::VARCHAR, '') FROM read_parquet('%s');
SQL
ROWS=$(duckdb -noheader -list -f /tmp/count.sql | tail -n 1)
if [ "$ROWS" != "%d" ]; then
  echo "archive holds $ROWS rows, expected %d: refusing to delete"
  exit 1
fi
NEWEST=$(duckdb -noheader -list -f /tmp/newest.sql | tail -n 1)
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
		settings, duckDBIdentifier(r.CutoffColumn), r.S3URI,
		r.PlannedRows, r.PlannedRows,
		r.CutoffColumn,
		cutoff, cutoff, cutoff)
}

// archiveRepackScript installs pg_repack if the database has never used it and
// rewrites the one table the run touched. ON_ERROR_STOP is on so a missing
// extension is reported by this Job rather than by pg_repack's own confusing
// "program version mismatch". Pure.
func archiveRepackScript(r archiveRun) string {
	table := r.Schema + "." + r.Table
	return fmt.Sprintf(`set -euo pipefail
psql -v ON_ERROR_STOP=1 -c "CREATE EXTENSION IF NOT EXISTS pg_repack"
pg_repack --no-superuser-check --table %q "$PGDATABASE"
`, table)
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
// and pg_repack (which takes no connection string at all) get their connection
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
duckdb -f /tmp/export.sql
`, archiveExportSQL(r, creds))
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

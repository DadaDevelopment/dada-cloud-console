package api

import (
	"strings"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/google/uuid"
)

func testArchiveRun() archiveRun {
	return archiveRun{
		ID:           uuid.MustParse("3f2c1a4e-9b7d-4c11-8a55-0e2d6f7a1b90"),
		ProjectID:    uuid.MustParse("11112222-3333-4444-5555-666677778888"),
		Datname:      "odds_research",
		Shard:        "pg-shard-0",
		Schema:       "public",
		Table:        "events",
		CutoffColumn: "created_at",
		Cutoff:       time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		PlannedRows:  1234567,
		S3URI:        "s3://dada-archive-111122223333/db-archive/x/data.parquet",
	}
}

func testArchiveCreds() cloudtask.S3Credentials {
	return cloudtask.S3Credentials{
		Endpoint:   "https://s3.ru1.storage.beget.cloud",
		AccessKey:  "AKIAEXAMPLEKEY",
		SecretKey:  "s3cr3t-do-not-log",
		BucketName: "dada-archive-111122223333",
	}
}

func TestRepackHasHeadroomFailsClosed(t *testing.T) {
	cases := []struct {
		name  string
		free  int64
		table int64
		want  bool
	}{
		{"unknown free space never fits", 0, 1 << 30, false},
		{"negative free space never fits", -1, 1 << 30, false},
		{"exactly the headroom fits", repackNeededBytes(1 << 30), 1 << 30, true},
		{"one byte short does not fit", repackNeededBytes(1<<30) - 1, 1 << 30, false},
		{"unknown table size with free space fits", 1 << 30, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := repackHasHeadroom(tc.free, tc.table); got != tc.want {
				t.Fatalf("repackHasHeadroom(%d, %d) = %v, want %v", tc.free, tc.table, got, tc.want)
			}
		})
	}
}

// TestRepackCopyBytes_MeasuresLiveRowsNotTheBloatedRelation pins the number the
// headroom guard is taken against.
//
// The delete leaves every page where it was, so the relation keeps its old size
// while the rewrite copies only what survived. Measured on Postgres 17: a
// 546 MB relation holding 10% of its rows rewrote into 55 MB, i.e. the live
// share, so a guard taken against the relation would refuse to reclaim exactly
// the databases too full to reclaim any other way.
func TestRepackCopyBytes_MeasuresLiveRowsNotTheBloatedRelation(t *testing.T) {
	const relation = 546 << 20
	got := repackCopyBytes(relation, 199_999, 1_800_001)
	if want := int64(55 << 20); got < want*9/10 || got > want*12/10 {
		t.Fatalf("repackCopyBytes = %s, want about %s (the measured rewrite)",
			humanBytes(got), humanBytes(want))
	}
	if got := repackCopyBytes(relation, 2_000_000, 0); got != relation {
		t.Fatalf("a run that deleted nothing copies the whole relation, got %s", humanBytes(got))
	}
	if got := repackCopyBytes(relation, 0, 2_000_000); got != 0 {
		t.Fatalf("an emptied table copies nothing, got %s", humanBytes(got))
	}
	if got := repackCopyBytes(0, 10, 10); got != 0 {
		t.Fatalf("unknown relation size stays unknown, got %d", got)
	}
}

func TestArchiveShardPVC(t *testing.T) {
	cases := map[string]string{
		"postgresql.databases.svc.cluster.local": "data-postgresql-0",
		"pg-shard-0.databases":                   "data-pg-shard-0-0",
		"pg-shard-0":                             "data-pg-shard-0-0",
		"":                                       "",
		"   ":                                    "",
	}
	for host, want := range cases {
		if got := archiveShardPVC(host); got != want {
			t.Fatalf("archiveShardPVC(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestArchiveNamesAreStable(t *testing.T) {
	r := testArchiveRun()
	if got, want := shortArchiveID(r.ID), "3f2c1a4e9b7d"; got != want {
		t.Fatalf("shortArchiveID = %q, want %q", got, want)
	}
	if got, want := archiveBucketName(r.ProjectID), "dada-archive-111122223333"; got != want {
		t.Fatalf("archiveBucketName = %q, want %q", got, want)
	}
	if got, want := archiveJobName(r.ID, dbArchiveExport), "db-archive-3f2c1a4e9b7d-export"; got != want {
		t.Fatalf("archiveJobName = %q, want %q", got, want)
	}
	uri := archiveObjectURI("dada-archive-111122223333", r)
	want := "s3://dada-archive-111122223333/db-archive/odds_research/public.events/2026-02-01-3f2c1a4e9b7d/data.parquet"
	if uri != want {
		t.Fatalf("archiveObjectURI = %q, want %q", uri, want)
	}
}

func TestArchiveObjectURIIsUniquePerRun(t *testing.T) {
	a := testArchiveRun()
	b := testArchiveRun()
	b.ID = uuid.MustParse("aaaabbbb-cccc-dddd-eeee-ffff00001111")
	if archiveObjectURI("bucket", a) == archiveObjectURI("bucket", b) {
		t.Fatal("two runs of the same table and cutoff must not share an object")
	}
}

func TestArchiveColumnUsable(t *testing.T) {
	cols := []archiveColumn{
		{Name: "created_at", Type: "timestamp with time zone"},
		{Name: "label", Type: "text"},
	}
	if !archiveColumnUsable(cols, "CREATED_AT") {
		t.Fatal("a timestamptz column must stay usable regardless of case")
	}
	if archiveColumnUsable(cols, "label") {
		t.Fatal("a text column cannot carry a cutoff")
	}
	if archiveColumnUsable(cols, "dropped_at") {
		t.Fatal("a column that no longer exists cannot carry a cutoff")
	}
}

func TestArchiveFreedBytes(t *testing.T) {
	if got := archiveFreedBytes(1000, 400); got != 600 {
		t.Fatalf("archiveFreedBytes(1000, 400) = %d, want 600", got)
	}
	if got := archiveFreedBytes(400, 1000); got != 0 {
		t.Fatalf("a table that grew must report zero freed, got %d", got)
	}
}

func TestArchiveExportPredicateMatchesTheDelete(t *testing.T) {
	r := testArchiveRun()
	sql := archiveExportSQL(r, testArchiveCreds())
	if !strings.Contains(sql, `"created_at" < DATE '2026-02-01'`) {
		t.Fatalf("export predicate is not the delete predicate:\n%s", sql)
	}
	if !strings.Contains(sql, `src."public"."events"`) {
		t.Fatalf("export does not read the run's table:\n%s", sql)
	}
	if !strings.Contains(sql, r.S3URI) {
		t.Fatalf("export does not write the run's object:\n%s", sql)
	}
	if !strings.Contains(sql, "FORMAT PARQUET") {
		t.Fatalf("export is not parquet:\n%s", sql)
	}
}

func TestArchiveVerifyScriptHoldsTheCountedRows(t *testing.T) {
	r := testArchiveRun()
	script := archiveVerifyScript(r, testArchiveCreds())
	if !strings.Contains(script, `!= "1234567"`) {
		t.Fatalf("verify does not compare against the planned row count:\n%s", script)
	}
	if !strings.Contains(script, `'<' "2026-02-01"`) {
		t.Fatalf("verify does not reject rows at or after the cutoff:\n%s", script)
	}
	if !strings.Contains(script, "set -euo pipefail") {
		t.Fatalf("verify does not abort on a failing duckdb invocation:\n%s", script)
	}
}

func TestArchiveScriptsCarryNoSecret(t *testing.T) {
	r := testArchiveRun()
	creds := testArchiveCreds()
	conn := archivePGConn{Host: "pg-shard-0.databases", Port: 5432, User: "postgres", Password: "pg-sup3r-secret", Database: r.Datname}

	scripts := map[string]string{
		"export": archiveExportSQL(r, creds),
		"verify": archiveVerifyScript(r, creds),
		"repack": archiveRepackScript(r),
	}
	for name, script := range scripts {
		for _, secret := range []string{creds.SecretKey, creds.AccessKey, conn.Password} {
			if strings.Contains(script, secret) {
				t.Fatalf("%s script leaks a credential into the Job spec:\n%s", name, script)
			}
		}
	}

	job := archiveExportJob("db-archive-x-export", r, creds, conn, defaultDBArchiveExportImage)
	env := map[string]string{}
	for _, e := range job.Spec.Template.Spec.Containers[0].Env {
		env[e.Name] = e.Value
	}
	if env["AWS_SECRET_ACCESS_KEY"] != creds.SecretKey {
		t.Fatal("the export job must receive the bucket key through the environment")
	}
	if env["PGPASSWORD"] != conn.Password {
		t.Fatal("the export job must receive the shard password through the environment")
	}
	if env["PGDATABASE"] != r.Datname {
		t.Fatalf("the export job attaches an empty DSN, so PGDATABASE must name the tenant database, got %q", env["PGDATABASE"])
	}
}

// TestArchiveDuckDBPhasesNeverCallABareBinary pins the failure that killed the
// first real export run: the CLI lives at /duckdb in the image and nothing puts
// it on PATH, so "duckdb -f ..." exited 127 and the run row only ever said
// BackoffLimitExceeded.
func TestArchiveDuckDBPhasesNeverCallABareBinary(t *testing.T) {
	r := testArchiveRun()
	creds := testArchiveCreds()
	conn := archivePGConn{Host: "pg-shard-0.databases", Port: 5432, User: "postgres", Password: "x", Database: r.Datname}

	scripts := map[string]string{
		"export": archiveExportJob("db-archive-x-export", r, creds, conn, defaultDBArchiveExportImage).Spec.Template.Spec.Containers[0].Command[2],
		"verify": archiveVerifyScript(r, creds),
	}
	for name, script := range scripts {
		if !strings.Contains(script, archiveDuckDBResolve) {
			t.Fatalf("%s script does not resolve the DuckDB binary:\n%s", name, script)
		}
		for _, line := range strings.Split(script, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "duckdb ") || strings.Contains(trimmed, "$(duckdb ") {
				t.Fatalf("%s script calls duckdb through PATH: %q", name, trimmed)
			}
		}
	}
}

func TestDuckDBEndpoint(t *testing.T) {
	cases := []struct {
		in     string
		host   string
		useSSL bool
	}{
		{"https://s3.ru1.storage.beget.cloud", "s3.ru1.storage.beget.cloud", true},
		{"http://minio.databases:9000", "minio.databases:9000", false},
		{"s3.ru1.storage.beget.cloud/", "s3.ru1.storage.beget.cloud", true},
	}
	for _, tc := range cases {
		host, useSSL := duckDBEndpoint(tc.in)
		if host != tc.host || useSSL != tc.useSSL {
			t.Fatalf("duckDBEndpoint(%q) = %q,%v want %q,%v", tc.in, host, useSSL, tc.host, tc.useSSL)
		}
	}
}

func TestDuckDBIdentifierQuoting(t *testing.T) {
	if got, want := duckDBIdentifier(`ev"il`), `"ev""il"`; got != want {
		t.Fatalf("duckDBIdentifier = %q, want %q", got, want)
	}
}

func TestArchiveJobSpecNeverRestartsInPlace(t *testing.T) {
	r := testArchiveRun()
	job := archiveRepackJob("db-archive-x-repack", r, archivePGConn{Host: "h", Port: 5432, Database: r.Datname}, defaultDBArchiveRepackImage)
	if job.Spec.Template.Spec.RestartPolicy != "Never" {
		t.Fatalf("archive phases must not restart in place, got %q", job.Spec.Template.Spec.RestartPolicy)
	}
	if job.Namespace != dbArchiveNamespace {
		t.Fatalf("archive job namespace = %q, want %q", job.Namespace, dbArchiveNamespace)
	}
	if *job.Spec.BackoffLimit != archiveJobBackoff {
		t.Fatalf("archive job backoff = %d, want %d", *job.Spec.BackoffLimit, archiveJobBackoff)
	}
}

func TestArchiveManifestTellsTheOwnerHowToReadIt(t *testing.T) {
	r := testArchiveRun()
	m := archiveManifest(r, 5<<30)
	if m["uri"] != r.S3URI {
		t.Fatalf("manifest uri = %v, want %v", m["uri"], r.S3URI)
	}
	if m["backupIntact"] != true {
		t.Fatal("manifest must say the backup still holds the archived rows")
	}
	for _, key := range []string{"readDuckDB", "readPandas"} {
		if s, _ := m[key].(string); !strings.Contains(s, r.S3URI) {
			t.Fatalf("manifest %s does not point at the archive: %v", key, m[key])
		}
	}
	if m["rows"] != r.PlannedRows {
		t.Fatalf("manifest rows = %v, want %v", m["rows"], r.PlannedRows)
	}
}

func monthBuckets(rows ...int64) []archiveBucket {
	out := make([]archiveBucket, 0, len(rows))
	for i, r := range rows {
		out = append(out, archiveBucket{
			Month: time.Date(2026, time.Month(i+1), 1, 0, 0, 0, 0, time.UTC),
			Rows:  r,
		})
	}
	return out
}

func TestAutoArchiveCutoffTakesTheLeastHistoryThatWorks(t *testing.T) {
	buckets := monthBuckets(100, 100, 100, 100)
	cutoff, ok := autoArchiveCutoff(buckets, 400, 4000, 1000)
	if !ok {
		t.Fatal("one month of four is enough to free a quarter of the table")
	}
	if want := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC); !cutoff.Equal(want) {
		t.Fatalf("cutoff = %s, want %s", cutoff, want)
	}
}

func TestAutoArchiveCutoffNeverTouchesTheNewestMonth(t *testing.T) {
	buckets := monthBuckets(10, 1000)
	if _, ok := autoArchiveCutoff(buckets, 1010, 10<<30, 9<<30); ok {
		t.Fatal("the month still being written to must never be archived automatically")
	}
	if _, ok := autoArchiveCutoff(monthBuckets(1000), 1000, 10<<30, 1<<30); ok {
		t.Fatal("a single-month table has nothing to archive automatically")
	}
}

func TestAutoArchiveCutoffRefusesWhenNothingIsWanted(t *testing.T) {
	if _, ok := autoArchiveCutoff(monthBuckets(100, 100, 100), 300, 3000, 0); ok {
		t.Fatal("a database that needs no space must not queue an archive")
	}
}

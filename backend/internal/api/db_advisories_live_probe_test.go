package api

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

type liveProbeSnapshot struct {
	UptimeSec float64 `json:"uptime_sec"`
	SizeBytes int64   `json:"size_bytes"`
	Tables    []struct {
		Schema        string     `json:"schema"`
		Name          string     `json:"name"`
		TotalBytes    int64      `json:"total_bytes"`
		HeapBytes     int64      `json:"heap_bytes"`
		IndexBytes    int64      `json:"index_bytes"`
		RowsEstimate  int64      `json:"rows_estimate"`
		NTupIns       int64      `json:"n_tup_ins"`
		NTupDel       int64      `json:"n_tup_del"`
		LastAutoanaly *time.Time `json:"last_autoanalyze"`
		HeapBlksRead  int64      `json:"heap_blks_read"`
		HeapBlksHit   int64      `json:"heap_blks_hit"`
	} `json:"tables"`
	Indexes []struct {
		Schema    string `json:"schema"`
		Table     string `json:"table"`
		Name      string `json:"name"`
		SizeBytes int64  `json:"size_bytes"`
		IdxScan   int64  `json:"idx_scan"`
		IsPrimary bool   `json:"is_primary"`
		IsUnique  bool   `json:"is_unique"`
	} `json:"indexes"`
	Statements []struct {
		QueryID int64   `json:"queryid"`
		Sample  string  `json:"sample"`
		Calls   int64   `json:"calls"`
		TotalMs float64 `json:"total_exec_time"`
		MeanMs  float64 `json:"mean_exec_time"`
	} `json:"statements"`
	StatementsTotalMs float64 `json:"statements_total_ms"`
}

// TestLiveProbe replays a snapshot taken from a real database through the
// advisory engine. It is skipped unless DB_ADVISORY_LIVE_SNAPSHOT points at a
// snapshot file, so it never runs in CI; it exists to check the acceptance
// criterion against live numbers instead of a fixture.
func TestLiveProbe(t *testing.T) {
	path := os.Getenv("DB_ADVISORY_LIVE_SNAPSHOT")
	if path == "" {
		t.Skip("DB_ADVISORY_LIVE_SNAPSHOT not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snap liveProbeSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}

	span := time.Duration(snap.UptimeSec) * time.Second
	in := dbAdvisoryInput{
		Shard:             "shard-1",
		Datname:           "odds-research",
		Now:               time.Now(),
		Uptime:            span,
		SizeBytes:         snap.SizeBytes,
		FirstSizeBytes:    snap.SizeBytes,
		SizeSpan:          span,
		StatementsTotalMs: snap.StatementsTotalMs,
	}
	for _, tb := range snap.Tables {
		in.Tables = append(in.Tables, dbAdvisoryTable{
			Schema: tb.Schema, Name: tb.Name,
			TotalBytes: tb.TotalBytes, HeapBytes: tb.HeapBytes, IndexBytes: tb.IndexBytes,
			RowsEstimate: tb.RowsEstimate, FirstTotalBytes: tb.TotalBytes, Span: span,
			DeltaTupIns: tb.NTupIns, DeltaTupDel: tb.NTupDel,
			DeltaHeapRead: tb.HeapBlksRead, DeltaHeapHit: tb.HeapBlksHit,
			LastAutoanalyze: tb.LastAutoanaly,
		})
	}
	for _, ix := range snap.Indexes {
		in.Indexes = append(in.Indexes, dbAdvisoryIndex{
			Schema: ix.Schema, Table: ix.Table, Name: ix.Name,
			SizeBytes: ix.SizeBytes, DeltaScans: ix.IdxScan, LatestScans: ix.IdxScan,
			Span: span, IsPrimary: ix.IsPrimary, IsUnique: ix.IsUnique,
		})
	}
	for _, s := range snap.Statements {
		in.Statements = append(in.Statements, dbAdvisoryStatement{
			QueryID: s.QueryID, Sample: s.Sample,
			DeltaCalls: s.Calls, DeltaTotalMs: s.TotalMs, MeanMs: s.MeanMs,
		})
	}

	for _, a := range evaluateDBAdvisories(in) {
		t.Logf("%-9s %-28s %s", a.Severity, a.Code, a.Subject)
		t.Logf("          %s", a.Detail)
	}
}

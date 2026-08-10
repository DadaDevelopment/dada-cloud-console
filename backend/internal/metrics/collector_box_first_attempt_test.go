package metrics

import (
	"context"
	"testing"

	"github.com/dada-tuda/console/backend/internal/dbtest"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// seedFirstAttemptProject creates a throwaway project holding exactly one box in
// the given status, where everReady decides whether that box carries a
// last_active_at stamp. It returns the project name, because the whole point of
// the gauge under test is that the name becomes a label.
func seedFirstAttemptProject(t *testing.T, pool *pgxpool.Pool, status string, everReady bool) string {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	name := "box-first-" + suffix

	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		name,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dbtest.DropProject(pool, projectID) })

	var envID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type, runtime)
		 VALUES ($1, $2, $3, 'dev', 'box') RETURNING id`,
		projectID, name, name+"-ns",
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO boxes (project_id, environment_id, name, image, profile, status, updated_at, last_active_at)
		 VALUES ($1, $2, $3, 'warm-v1', 'box-standard', $4, now(),
		         CASE WHEN $5 THEN now() ELSE NULL END)`,
		projectID, envID, name, status, everReady,
	); err != nil {
		t.Fatalf("seed box: %v", err)
	}
	return name
}

// hasFirstAttemptSeries reports whether the gauge currently publishes a series for
// project. Absence and zero are different answers here — the collector Resets and
// only writes the projects that qualify — so this walks the published series
// rather than reading a value that would be indistinguishable from "never set".
func hasFirstAttemptSeries(t *testing.T, project string) bool {
	t.Helper()
	ch := make(chan prometheus.Metric, 256)
	go func() {
		boxFirstAttemptFailed.Collect(ch)
		close(ch)
	}()
	for m := range ch {
		var d dto.Metric
		if err := m.Write(&d); err != nil {
			t.Fatalf("read gauge: %v", err)
		}
		for _, l := range d.GetLabel() {
			if l.GetName() == "project" && l.GetValue() == project {
				return true
			}
		}
	}
	return false
}

// TestFirstAttemptFailureNamesTheProjectThatOnlyEverSawFailure is the contract the
// unlabelled dada_box_failed_recent cannot express, and the reason this gauge was
// added: it separates a stranger whose first and only box died from a project that
// has had boxes working all along.
//
// The control half is the important half. Both projects move
// dada_box_failed_recent by exactly 1, which is precisely how the first real box
// customer's failure was indistinguishable from our own test junk.
func TestFirstAttemptFailureNamesTheProjectThatOnlyEverSawFailure(t *testing.T) {
	pool := testCollectorPool(t)
	ctx := context.Background()

	collectBoxes(ctx, pool)
	beforeFailedRecent := gaugeValue(t, boxFailedRecent)

	stranger := seedFirstAttemptProject(t, pool, "Failed", false)
	collectBoxes(ctx, pool)

	if !hasFirstAttemptSeries(t, stranger) {
		t.Errorf("no dada_box_first_attempt_failed series for %q, but its only box ever failed", stranger)
	}
	if got := gaugeValue(t, boxFailedRecent) - beforeFailedRecent; got != 1 {
		t.Fatalf("dada_box_failed_recent moved by %v, want 1 (test seed is wrong, not the gauge)", got)
	}
}

// TestFirstAttemptFailureIgnoresProjectsThatHaveHadAWorkingBox keeps the alert
// about first impressions. A project whose box reached Ready once has a working
// relationship with the product; its later failure is an error rate, and the noisy
// version of this gauge would page about it forever.
//
// "Reached Ready" is read from last_active_at because markBoxReady
// (internal/api/box_boot.go) is the only writer that stamps it, so the signal
// survives the box being deleted afterwards.
func TestFirstAttemptFailureIgnoresProjectsThatHaveHadAWorkingBox(t *testing.T) {
	pool := testCollectorPool(t)
	ctx := context.Background()

	collectBoxes(ctx, pool)
	beforeFailedRecent := gaugeValue(t, boxFailedRecent)

	returning := seedFirstAttemptProject(t, pool, "Failed", true)
	collectBoxes(ctx, pool)

	if hasFirstAttemptSeries(t, returning) {
		t.Errorf("dada_box_first_attempt_failed has a series for %q, but that project has had a box reach Ready; "+
			"this alert is about first impressions, not error rates", returning)
	}
	if got := gaugeValue(t, boxFailedRecent) - beforeFailedRecent; got != 1 {
		t.Errorf("dada_box_failed_recent moved by %v, want 1 — the old gauge is supposed to count this "+
			"failure identically, which is exactly why the labelled one exists", got)
	}
}

// TestFirstAttemptFailureClearsWhenTheProjectFinallyGetsAWorkingBox proves the
// series is a live state and not a tombstone: the collector Resets each pass, so a
// project that succeeds stops being alerted on instead of freezing at 1 forever.
func TestFirstAttemptFailureClearsWhenTheProjectFinallyGetsAWorkingBox(t *testing.T) {
	pool := testCollectorPool(t)
	ctx := context.Background()

	stranger := seedFirstAttemptProject(t, pool, "Failed", false)
	collectBoxes(ctx, pool)
	if !hasFirstAttemptSeries(t, stranger) {
		t.Fatalf("no series for %q before the retry; the rest of this test would prove nothing", stranger)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE boxes SET last_active_at = now()
		  WHERE project_id = (SELECT id FROM projects WHERE name = $1)`, stranger); err != nil {
		t.Fatalf("stamp a successful boot: %v", err)
	}
	collectBoxes(ctx, pool)

	if hasFirstAttemptSeries(t, stranger) {
		t.Errorf("dada_box_first_attempt_failed still names %q after a box reached Ready", stranger)
	}
}

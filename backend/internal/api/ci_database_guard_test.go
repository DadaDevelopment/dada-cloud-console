package api

import (
	"os"
	"strings"
	"testing"
)

// TestCIRequiresDatabase makes the DB-backed test suite fail loudly in CI when
// there is no database, instead of skipping quietly.
//
// Why this exists: for a long stretch the Jenkins pod template had no postgres
// container and never set TEST_DATABASE_URL, so every test guarded by a
// `t.Skip("TEST_DATABASE_URL not set")` helper — advisory locks, quota gates,
// storage caps, deploy hooks, billing expiry, optimistic snapshots and more —
// skipped in CI. The "Backend tests" stage went green while testing none of them.
// A gate that can silently become decoration is worse than no gate, because it
// buys false confidence.
//
// Skipping locally is still correct and stays supported: a developer running
// `go test ./...` without docker should not be blocked. The distinction is CI,
// where a missing database is a broken pipeline rather than a missing convenience.
func TestCIRequiresDatabase(t *testing.T) {
	if !isCI() {
		t.Skip("not running in CI; TEST_DATABASE_URL is optional for local runs")
	}
	if strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")) == "" {
		t.Fatal("TEST_DATABASE_URL is empty in CI: every DB-backed test in this package " +
			"is skipping, so this stage is not testing what it claims to. Restore the postgres " +
			"container in the Jenkinsfile pod template and the TEST_DATABASE_URL wiring in the " +
			"'Backend tests' stage.")
	}
}

// isCI reports whether we are running inside a CI job. CI=true is set by Jenkins,
// GitHub Actions and essentially every other runner; JENKINS_URL is checked too so
// the guard still works if someone trims the environment.
func isCI() bool {
	if os.Getenv("JENKINS_URL") != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CI"))) {
	case "", "0", "false":
		return false
	default:
		return true
	}
}

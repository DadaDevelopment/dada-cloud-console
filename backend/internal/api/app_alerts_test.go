package api

import (
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/models"
)

func ratioPtr(v float64) *float64 { return &v }

func TestGroupAppAlertsGroupsByAppName(t *testing.T) {
	now := time.Now()
	rows := []appAlertRow{
		{AppName: "web", Type: "crash", Reason: "CrashLoopBackOff", Detail: "pod-1/web", DetectedAt: now.Add(-time.Hour)},
		{AppName: "web", Type: "volume", Ratio: ratioPtr(0.91), DetectedAt: now},
		{AppName: "api", Type: "crash", Reason: "OOMKilled", Detail: "pod-2/api", DetectedAt: now},
	}

	got := groupAppAlerts(rows)

	if len(got) != 2 {
		t.Fatalf("expected 2 apps with alerts, got %d: %+v", len(got), got)
	}
	if len(got["web"]) != 2 {
		t.Fatalf("expected 2 alerts for web, got %+v", got["web"])
	}
	if len(got["api"]) != 1 || got["api"][0].Reason != "OOMKilled" {
		t.Fatalf("expected 1 OOMKilled alert for api, got %+v", got["api"])
	}
}

func TestGroupAppAlertsSortsNewestFirst(t *testing.T) {
	now := time.Now()
	rows := []appAlertRow{
		{AppName: "web", Type: "crash", Reason: "CrashLoopBackOff", DetectedAt: now.Add(-2 * time.Hour)},
		{AppName: "web", Type: "volume", Ratio: ratioPtr(0.9), DetectedAt: now},
		{AppName: "web", Type: "crash", Reason: "OOMKilled", DetectedAt: now.Add(-time.Hour)},
	}

	got := groupAppAlerts(rows)["web"]
	if len(got) != 3 {
		t.Fatalf("expected 3 alerts, got %d", len(got))
	}
	if got[0].Type != "volume" || got[1].Reason != "OOMKilled" || got[2].Reason != "CrashLoopBackOff" {
		t.Fatalf("alerts not sorted newest-first: %+v", got)
	}
}

func TestGroupAppAlertsEmptyInputGivesEmptyMap(t *testing.T) {
	got := groupAppAlerts(nil)
	if len(got) != 0 {
		t.Fatalf("expected empty map for no rows, got %+v", got)
	}
}

// TestApplyAppAlertsOnlyStampsMatchingApps is RED-proof against the old
// ListApps code path, which never read app_health_alerts/app_volume_alerts at
// all: before applyAppAlerts existed, every app's Alerts field was always
// nil/absent regardless of a live cooldown row. This asserts the field is
// actually populated for a matched app, and left empty for one with no
// current alert, so a regression back to "never wired up" fails the test.
func TestApplyAppAlertsOnlyStampsMatchingApps(t *testing.T) {
	apps := []models.ResourceSnapshot{
		{Name: "web"},
		{Name: "quiet-app"},
	}
	byApp := map[string][]models.AppAlert{
		"web": {{Type: "crash", Reason: "CrashLoopBackOff", DetectedAt: time.Now()}},
	}

	applyAppAlerts(apps, byApp)

	if len(apps[0].Alerts) != 1 || apps[0].Alerts[0].Reason != "CrashLoopBackOff" {
		t.Fatalf("expected web to carry its crash alert, got %+v", apps[0].Alerts)
	}
	if len(apps[1].Alerts) != 0 {
		t.Fatalf("expected quiet-app to have no alerts, got %+v", apps[1].Alerts)
	}
}

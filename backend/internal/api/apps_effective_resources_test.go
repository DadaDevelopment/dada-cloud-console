package api

import (
	"encoding/json"
	"testing"

	"github.com/dada-tuda/console/backend/internal/models"
)

func summaryOf(t *testing.T, rs models.ResourceSnapshot) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rs.SummaryJSON, &m); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	return m
}

func snapshotWith(t *testing.T, summary map[string]any) models.ResourceSnapshot {
	t.Helper()
	b, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	return models.ResourceSnapshot{Kind: "App", Name: "x", SummaryJSON: b}
}

// TestFillEffectiveResourcesKeepsObservedTruth is the anti-fabrication guard:
// an app that never used the profile mechanism, but whose real envelope the
// status reconciler measured, must not be handed the "small" profile's numbers
// as if they were its own.
func TestFillEffectiveResourcesKeepsObservedTruth(t *testing.T) {
	apps := []models.ResourceSnapshot{snapshotWith(t, map[string]any{
		"observed_resources": map[string]any{"cpu_limit": "2", "memory_limit": "4Gi"},
	})}
	FillEffectiveResources(apps)

	if _, ok := summaryOf(t, apps[0])["resources"]; ok {
		t.Fatalf("FillEffectiveResources invented a profile envelope: %s", apps[0].SummaryJSON)
	}
}

// TestFillEffectiveResourcesStillDefaultsUnobservedApps keeps the original
// behaviour for the case it was written for: an app with neither an explicit
// envelope nor an observation still resolves the renderer's default profile.
func TestFillEffectiveResourcesStillDefaultsUnobservedApps(t *testing.T) {
	apps := []models.ResourceSnapshot{snapshotWith(t, map[string]any{"image": "app:1"})}
	FillEffectiveResources(apps)

	res, ok := summaryOf(t, apps[0])["resources"].(map[string]any)
	if !ok {
		t.Fatalf("no resources filled in: %s", apps[0].SummaryJSON)
	}
	if res["cpu_limit"] == "" || res["memory_limit"] == "" {
		t.Fatalf("filled envelope is empty: %v", res)
	}
}

// TestFillEffectiveResourcesHonoursExplicitProfile proves an app that really
// does carry a profile keeps resolving it even when an observation exists —
// the profile is a user-visible setting, the observation is a measurement.
func TestFillEffectiveResourcesHonoursExplicitProfile(t *testing.T) {
	apps := []models.ResourceSnapshot{snapshotWith(t, map[string]any{
		"profile":            "small",
		"observed_resources": map[string]any{"cpu_limit": "2"},
	})}
	FillEffectiveResources(apps)

	if _, ok := summaryOf(t, apps[0])["resources"]; !ok {
		t.Fatalf("explicit profile was not resolved: %s", apps[0].SummaryJSON)
	}
}

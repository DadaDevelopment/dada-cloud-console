package worker

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// adoptedFindataDesired mirrors the shape adoption writes for a service of an
// existing hand-authored stack: the flat fields plus the VERBATIM compose block.
func adoptedFindataDesired(image string) map[string]any {
	return map[string]any{
		"image": image,
		"ports": []any{"8000:8000"},
		"compose": map[string]any{
			"image":       image,
			"restart":     "unless-stopped",
			"ports":       []any{"8000:8000"},
			"env_file":    []any{".env"},
			"depends_on":  []any{"postgres"},
			"environment": map[string]any{"DB_URL": "postgres://profi@postgres:5432/profi"},
		},
	}
}

// TestReleaseOnAdoptedAppChangesTheRenderedImage is the guard for the VM release
// path: POST /api/v1/deploy on a runtime=vm environment lands in
// updateComposeAppImage, and the app it targets on findata is ADOPTED. Writing
// only desired.image leaves the verbatim compose block — the one the renderer
// actually uses — pointing at the previous build, so the deploy succeeds while
// the VM keeps serving the old image.
func TestReleaseOnAdoptedAppChangesTheRenderedImage(t *testing.T) {
	desired := adoptedFindataDesired("ghcr.io/acme/backend:522")

	setComposeDesiredImage(desired, "ghcr.io/acme/backend:523")

	if got := desired["image"]; got != "ghcr.io/acme/backend:523" {
		t.Errorf("desired.image = %v, want the released image", got)
	}
	compose, _ := desired["compose"].(map[string]any)
	if compose == nil {
		t.Fatal("desired.compose was dropped; the adopted service block is the deployed one")
	}
	if got := compose["image"]; got != "ghcr.io/acme/backend:523" {
		t.Errorf("desired.compose.image = %v, want the released image — the renderer reads THIS, not desired.image", got)
	}

	for _, key := range []string{"restart", "env_file", "depends_on", "environment"} {
		if _, ok := compose[key]; !ok {
			t.Errorf("compose block lost %q; a release must not rewrite the adopted service", key)
		}
	}

	spec := renderer.AppServiceSpec{AppName: "backend", Image: desired["image"].(string), Service: compose}
	agg, err := renderer.RenderAggregateCompose([]renderer.AppServiceSpec{spec}, nil)
	if err != nil {
		t.Fatalf("render aggregate: %v", err)
	}
	if !strings.Contains(agg, "ghcr.io/acme/backend:523") {
		t.Errorf("aggregate does not carry the released image:\n%s", agg)
	}
	if strings.Contains(agg, "ghcr.io/acme/backend:522") {
		t.Errorf("aggregate still carries the previous image:\n%s", agg)
	}
}

// TestReleaseOnAuthoredAppNeedsNoComposeBlock keeps the helper honest for the
// authored case, where desired.compose is absent and desired.image is what the
// renderer builds the service from.
func TestReleaseOnAuthoredAppNeedsNoComposeBlock(t *testing.T) {
	desired := map[string]any{"image": "ghcr.io/acme/api:1"}

	setComposeDesiredImage(desired, "ghcr.io/acme/api:2")

	if got := desired["image"]; got != "ghcr.io/acme/api:2" {
		t.Errorf("desired.image = %v, want the released image", got)
	}
	if _, ok := desired["compose"]; ok {
		t.Error("a compose block was invented for an authored app; that would freeze it as adopted")
	}
}

// TestReleaseSurvivesSnapshotRoundTrip asserts the patch holds through the JSON
// the snapshot is actually stored as: desired arrives from summary_json as
// map[string]any, and the compose block must still be addressable after the
// round trip or the release silently degrades to the flat-field-only case.
func TestReleaseSurvivesSnapshotRoundTrip(t *testing.T) {
	summary := map[string]any{"desired": adoptedFindataDesired("ghcr.io/acme/backend:522")}
	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	cur := map[string]any{}
	if err := json.Unmarshal(raw, &cur); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}

	desired := composeDesiredMap(cur)
	setComposeDesiredImage(desired, "ghcr.io/acme/backend:523")
	cur["desired"] = desired

	out, err := json.Marshal(cur)
	if err != nil {
		t.Fatalf("marshal patched snapshot: %v", err)
	}
	var back struct {
		Desired composeDesired `json:"desired"`
	}
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal patched snapshot: %v", err)
	}
	if back.Desired.Image != "ghcr.io/acme/backend:523" {
		t.Errorf("desired.image = %q after round trip", back.Desired.Image)
	}
	if got := back.Desired.Compose["image"]; got != "ghcr.io/acme/backend:523" {
		t.Errorf("desired.compose.image = %v after round trip", got)
	}
}

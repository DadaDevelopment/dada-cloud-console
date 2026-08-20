package worker

import "testing"

// TestComposeEnvApplyIsRecognisedByAnAbsentImage pins the contract the console
// and the worker share for VM apps: an operation that names no image delivers
// configuration by re-assembling the stack at whatever the snapshot already
// holds, and must never patch the desired image. Saving an env var on
// fin-core/findata used to carry the last image the console had RECORDED, which
// had drifted from the tag actually serving traffic, so editing a variable
// would have shipped a different build of a live customer site.
func TestComposeEnvApplyIsRecognisedByAnAbsentImage(t *testing.T) {
	for _, image := range []string{"", "   ", "\t\n"} {
		if !isComposeEnvApply(image) {
			t.Errorf("image %q read as a release; an operation with no image is a config-apply", image)
		}
	}
	for _, image := range []string{
		"nexus.dada-tuda.ru/dada/profi-backend:master-1.0.0-44",
		"ghcr.io/acme/api@sha256:1111111111111111111111111111111111111111111111111111111111111111",
	} {
		if isComposeEnvApply(image) {
			t.Errorf("image %q read as a config-apply; a named tag is a release and must move the desired image", image)
		}
	}
}

// TestComposeEnvApplyLeavesTheDesiredImageAlone drives the real snapshot-write
// helper: a config-apply must return the snapshot untouched, while a release
// with the same helper moves the tag the renderer reads.
func TestComposeEnvApplyLeavesTheDesiredImageAlone(t *testing.T) {
	const running = "nexus.dada-tuda.ru/dada/profi-backend:master-1.0.0-41"
	const recorded = "nexus.dada-tuda.ru/dada/profi-backend:master-1.0.0-44"

	cur := map[string]any{"desired": adoptedFindataDesired(running)}
	cur = composeSnapshotForRelease(cur, "")

	desired, _ := cur["desired"].(map[string]any)
	compose, _ := desired["compose"].(map[string]any)
	if got := compose["image"]; got != running {
		t.Errorf("config-apply moved desired.compose.image to %v; the stack must re-assemble at %q", got, running)
	}
	if got := desired["image"]; got != running {
		t.Errorf("config-apply moved desired.image to %v, want %q", got, running)
	}
	if _, marked := cur["status"]; marked {
		t.Error("config-apply marked the snapshot Pending; it releases nothing")
	}

	released := composeSnapshotForRelease(map[string]any{"desired": adoptedFindataDesired(running)}, recorded)
	rd, _ := released["desired"].(map[string]any)
	rc, _ := rd["compose"].(map[string]any)
	if rc["image"] != recorded || rd["image"] != recorded {
		t.Errorf("release did not move the desired image: flat=%v compose=%v, want %q", rd["image"], rc["image"], recorded)
	}
}

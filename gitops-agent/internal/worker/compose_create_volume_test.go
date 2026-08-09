package worker

import (
	"strings"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// A stateful catalog entry installed onto a VM must reach the stack with its
// data directory mounted. Before this, the API rejected the volume outright and
// every ready-made project that keeps state was undeployable on a VM; a payload
// that silently loses the mount would be worse — the app comes up green and
// loses its data on the next redeploy.
func TestComposeDesiredFromCreateMountsTheDataVolume(t *testing.T) {
	payload := []byte(`{"name":"gitea","image":"gitea/gitea:1.27","port":3000,"volume":{"path":"/data","size":"20Gi","storage_class":"longhorn-dev","fs_group":1000}}`)

	got := composeDesiredFromCreate(payload, "gitea")

	if got.Image != "gitea/gitea:1.27" {
		t.Fatalf("image = %q", got.Image)
	}
	if len(got.Ports) != 1 || got.Ports[0] != "3000:3000" {
		t.Fatalf("ports = %v", got.Ports)
	}
	if len(got.Volumes) != 1 || got.Volumes[0] != "gitea-data:/data" {
		t.Fatalf("volumes = %v", got.Volumes)
	}
}

func TestComposeDesiredFromCreateWithoutVolume(t *testing.T) {
	got := composeDesiredFromCreate([]byte(`{"image":"nginx:1.29","port":80}`), "web")
	if len(got.Volumes) != 0 {
		t.Fatalf("volumes = %v, want none", got.Volumes)
	}
}

// The named volume must be pinned external by the aggregate renderer, and
// reported to the deploy worker for creation — otherwise the first deploy fails
// on a missing volume, or a redeploy quietly attaches a fresh empty one.
func TestCreatedComposeVolumeIsPinnedExternal(t *testing.T) {
	desired := composeDesiredFromCreate([]byte(`{"image":"gitea/gitea:1.27","port":3000,"volume":{"path":"/data"}}`), "gitea")
	specs := []renderer.AppServiceSpec{{
		AppName: "gitea",
		Image:   desired.Image,
		Ports:   desired.Ports,
		Volumes: desired.Volumes,
	}}

	agg, err := renderer.RenderAggregateCompose(specs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(agg, "gitea-data:/data") {
		t.Fatalf("aggregate does not mount the data volume:\n%s", agg)
	}
	if !strings.Contains(agg, "external: true") {
		t.Fatalf("aggregate does not pin the volume external:\n%s", agg)
	}

	named := renderer.AuthoredNamedVolumes(specs)
	if len(named) != 1 || named[0] != "gitea-data" {
		t.Fatalf("authored named volumes = %v", named)
	}
}

package worker

import (
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// tagOnlyImageValues is the shape internal/gateway and internal/user carry: the
// image NAME comes from the chart default and only the tag is pinned by hand.
// The pin is load-bearing -- Nexus retention GC's the mutable SNAPSHOT tags, so
// the surviving tag is chosen deliberately -- and common.image is a key the
// console claims, so a render that adopted nothing but the name replaces the
// block with an empty one and the app silently falls back to the chart default.
const tagOnlyImageValues = `common:
  image:
    tag: develop-0.0.1-SNAPSHOT-27
  servicePort: 8080
  replicas: 1
  resources:
    requests:
      cpu: 600m
      memory: 512Mi
      ephemeral-storage: 50Mi
    limits:
      cpu: "4"
      memory: 768Mi
      ephemeral-storage: 200Mi
`

// unmodelledResourceKeyValues is internal/reels-tracker: a camelCase
// ephemeralStorage sits next to the canonical ephemeral-storage inside
// common.resources. The console has no field for it, and common.resources is a
// key the console claims, so a render that knows only cpu/memory/ephemeral-storage
// replaces the whole block and deletes it.
const unmodelledResourceKeyValues = `common:
  image:
    name: nexus.dada-tuda.ru/dada/reels-tracker
    tag: master-1.0.0-9
  servicePort: 8000
  replicas: 1
  resources:
    requests:
      cpu: 80m
      memory: 640Mi
      ephemeralStorage: 200Mi
      ephemeral-storage: 200Mi
    limits:
      cpu: 800m
      memory: 768Mi
      ephemeralStorage: 1Gi
      ephemeral-storage: 1Gi
`

func TestAdoptedTagOnlyImageSurvivesTheNextWrite(t *testing.T) {
	merged := adoptRenderMerge(t, "gateway", tagOnlyImageValues)

	if dropped := renderer.DroppedPaths(tagOnlyImageValues, merged); len(dropped) > 0 {
		t.Fatalf("the write after adoption deletes %v", dropped)
	}
	after := parseAppShape(t, merged)
	if after.Common.Image.Tag != "develop-0.0.1-SNAPSHOT-27" {
		t.Errorf("image tag %q: losing the pin drops the app onto the chart default tag", after.Common.Image.Tag)
	}
	if after.Common.Image.Name != "" {
		t.Errorf("image name %q: git names none, so a render that invents one rewrites the block", after.Common.Image.Name)
	}
}

func TestAdoptedUnmodelledResourceKeysSurviveTheNextWrite(t *testing.T) {
	merged := adoptRenderMerge(t, "reels-tracker", unmodelledResourceKeyValues)

	if dropped := renderer.DroppedPaths(unmodelledResourceKeyValues, merged); len(dropped) > 0 {
		t.Fatalf("the write after adoption deletes %v", dropped)
	}
	after := parseAppShape(t, merged)
	if got := after.Common.Resources.Requests["ephemeralStorage"]; got != "200Mi" {
		t.Errorf("requests.ephemeralStorage = %q, want 200Mi", got)
	}
	if got := after.Common.Resources.Limits["ephemeralStorage"]; got != "1Gi" {
		t.Errorf("limits.ephemeralStorage = %q, want 1Gi", got)
	}
	if got := after.Common.Resources.Limits["ephemeral-storage"]; got != "1Gi" {
		t.Errorf("limits.ephemeral-storage = %q, want 1Gi", got)
	}
}

// adoptRenderMerge does to a values.yaml exactly what the console does on the
// first write after adoption: read the app out of git, render from what was
// read, merge the render back onto git.
func adoptRenderMerge(t *testing.T, appName, existing string) string {
	t.Helper()
	adopted, err := parseAdoptableValues(existing)
	if err != nil {
		t.Fatalf("parseAdoptableValues: %v", err)
	}
	spec := renderer.AppSpec{
		Name:         appName,
		Image:        adopted.Image,
		Port:         adopted.ServicePort,
		Resources:    adopted.Resources,
		Profile:      "small",
		WorkloadType: adopted.WorkloadType,
		StartCommand: adopted.StartCommand,
	}
	if adopted.Replicas != nil {
		spec.Replicas = *adopted.Replicas
	}
	if v := adopted.Volume; v != nil {
		spec.VolumePath = v.Path
		spec.VolumeSize = v.Size
		spec.VolumeStorageClass = v.StorageClass
		spec.VolumeFSGroup = v.FSGroup
	}
	env := resolvedEnv{Plain: adopted.Plain, Secret: map[string]string{}, Refs: adopted.Refs}
	env.applyTo(&spec, appName)

	prev := renderer.PgRouterClusterIP
	renderer.PgRouterClusterIP = "10.96.139.238"
	defer func() { renderer.PgRouterClusterIP = prev }()

	rendered, err := renderer.RenderAppValues(spec)
	if err != nil {
		t.Fatalf("RenderAppValues: %v", err)
	}
	merged, err := renderer.MergeAppValuesWith(existing, rendered, renderer.MergeOptions{})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	return merged
}

package worker

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// oomSizedValues is leadgen/prod/lead-gen as it stood in argo-infra after
// commit ebea3096: the memory envelope was raised by hand because the app runs
// a headless browser that the console's 256Mi limit OOM-killed mid-scan. The
// console has no field that can express this size -- its user-facing ladder
// tops out at 1Gi -- so the number lives in git and nowhere else.
const oomSizedValues = `common:
  image:
    name: nexus.dada-tuda.ru/dada/lead-gen
    tag: master-1.0.0-17
  servicePort: 8000
  resources:
    requests:
      cpu: 40m
      memory: 384Mi
    limits:
      cpu: "1"
      memory: 1536Mi
`

// renderWithConsoleEnvelope is the render a console deploy produces for that
// app: the database still carries the small envelope the app was created with.
func renderWithConsoleEnvelope(t *testing.T) string {
	t.Helper()
	rendered, err := renderer.RenderAppValues(renderer.AppSpec{
		Name:     "lead-gen",
		Image:    "nexus.dada-tuda.ru/dada/lead-gen:master-1.0.0-18",
		Replicas: 1,
		Port:     8000,
		Resources: &renderer.AppResources{
			CPURequest:    "10m",
			MemoryRequest: "128Mi",
			CPULimit:      "250m",
			MemoryLimit:   "256Mi",
		},
	})
	if err != nil {
		t.Fatalf("RenderAppValues: %v", err)
	}
	return rendered
}

func mergedCommon(t *testing.T, existing, rendered string, cur map[string]any) map[string]any {
	t.Helper()
	merged, err := renderer.MergeAppValuesWith(existing, rendered, renderer.MergeOptions{
		Advisory: advisoryValuesKeys(cur),
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(merged), &doc); err != nil {
		t.Fatalf("merged values do not parse: %v\n%s", err, merged)
	}
	c, _ := doc["common"].(map[string]any)
	if c == nil {
		t.Fatalf("no common mapping: %s", merged)
	}
	return c
}

func memoryLimit(t *testing.T, common map[string]any) any {
	t.Helper()
	res, _ := common["resources"].(map[string]any)
	if res == nil {
		return nil
	}
	limits, _ := res["limits"].(map[string]any)
	if limits == nil {
		return nil
	}
	return limits["memory"]
}

// TestDeployDoesNotShrinkAnEnvelopeGitAlreadyHolds is the regression test for
// 2026-08-25: an agent raised lead-gen to 1536Mi in argo-infra to stop a
// headless browser being OOM-killed, and reported that any console write puts
// 256Mi back. common.resources is an owned key, so every render asserts the
// console's envelope over whatever is in the file -- as an in-place CHANGE, so
// the clobber guard cannot see it and the adoption it now triggers never fires.
// A deploy is about an image; ResizeApp is the operation about size, and it
// patches the scalars in the file already in git.
func TestDeployDoesNotShrinkAnEnvelopeGitAlreadyHolds(t *testing.T) {
	for _, portSource := range []string{appPortSourceUser, "framework_default"} {
		t.Run(portSource, func(t *testing.T) {
			common := mergedCommon(t, oomSizedValues, renderWithConsoleEnvelope(t),
				map[string]any{"port_source": portSource})
			if got := memoryLimit(t, common); got != "1536Mi" {
				t.Errorf("memory limit = %v, want the 1536Mi that is in git", got)
			}
			res, _ := common["resources"].(map[string]any)
			requests, _ := res["requests"].(map[string]any)
			if requests["memory"] != "384Mi" {
				t.Errorf("memory request = %v, want the 384Mi that is in git", requests["memory"])
			}
		})
	}
}

// TestNewAppStillGetsItsRenderedEnvelope holds the other pole: advisory means
// silent where git speaks, not silent everywhere. An app whose values.yaml has
// no resources block -- a freshly created one, or one adopted from a file that
// never sized itself -- must still receive the envelope, or the chart defaults
// decide the app's size.
func TestNewAppStillGetsItsRenderedEnvelope(t *testing.T) {
	const noResources = `common:
  image:
    name: nexus.dada-tuda.ru/dada/lead-gen
    tag: master-1.0.0-17
  servicePort: 8000
`
	common := mergedCommon(t, noResources, renderWithConsoleEnvelope(t),
		map[string]any{"port_source": appPortSourceUser})
	if got := memoryLimit(t, common); got != "256Mi" {
		t.Errorf("memory limit = %v, want the rendered 256Mi where git is silent", got)
	}
}

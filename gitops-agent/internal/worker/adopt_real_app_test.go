package worker

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// TestAdoptedRealAppRendersAsItself runs adoption against the actual
// values.yaml of internal/prod/telemost-bot -- the app a console write broke on
// 2026-08-21 -- and then does what the console does on its next write: render
// from the adopted state and merge that render onto the file in git.
//
// The bar is that the merged file still describes the same app. The values
// merge alone cannot deliver that: it protects only keys the console has never
// heard of, and for a key the console CLAIMS (image, servicePort, replicas,
// resources, extraEnv) it writes its own copy over git's. The clobber guard
// does not catch that either, because it measures deletions and a replacement
// deletes nothing. Adoption is what makes the console's copy equal to git's.
//
// The fixture is the real file with the three plaintext tokens in envFileValue
// replaced by a placeholder: the block has to be present to prove the merge
// keeps it, but a credential does not have to be copied into a second repo to
// prove that.
func TestAdoptedRealAppRendersAsItself(t *testing.T) {
	existing := readFixture(t, "testdata/telemost-bot-values.yaml")

	adopted, err := parseAdoptableValues(existing)
	if err != nil {
		t.Fatalf("parseAdoptableValues: %v", err)
	}

	env := resolvedEnv{Plain: adopted.Plain, Secret: map[string]string{}, Refs: adopted.Refs}
	spec := renderer.AppSpec{
		Name:      "telemost-bot",
		Image:     adopted.Image,
		Port:      adopted.ServicePort,
		Resources: adopted.Resources,
		Profile:   "small",
	}
	if adopted.Replicas != nil {
		spec.Replicas = *adopted.Replicas
	}
	env.applyTo(&spec, "telemost-bot")

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

	if dropped := renderer.DroppedPaths(existing, merged); len(dropped) > 0 {
		t.Fatalf("the write after adoption still deletes %v", dropped)
	}

	before := parseAppShape(t, existing)
	after := parseAppShape(t, merged)

	if before.Common.ServicePort != after.Common.ServicePort {
		t.Errorf("servicePort %d -> %d", before.Common.ServicePort, after.Common.ServicePort)
	}
	if before.Common.Image != after.Common.Image {
		t.Errorf("image %+v -> %+v", before.Common.Image, after.Common.Image)
	}
	if before.Common.Replicas != after.Common.Replicas {
		t.Errorf("replicas %d -> %d", before.Common.Replicas, after.Common.Replicas)
	}
	if before.Common.UseDotEnv != after.Common.UseDotEnv {
		t.Errorf("useDotEnv %q -> %q: the .env mount carries the bot's Telegram token", before.Common.UseDotEnv, after.Common.UseDotEnv)
	}
	if !equalStringMap(before.Common.Resources.Requests, after.Common.Resources.Requests) ||
		!equalStringMap(before.Common.Resources.Limits, after.Common.Resources.Limits) {
		t.Errorf("resources %+v -> %+v", before.Common.Resources, after.Common.Resources)
	}
	if len(before.Common.EnvFileValue) != len(after.Common.EnvFileValue) {
		t.Errorf("envFileValue went from %d keys to %d", len(before.Common.EnvFileValue), len(after.Common.EnvFileValue))
	}
	if before.Common.ServiceDatabase.Name != after.Common.ServiceDatabase.Name {
		t.Errorf("serviceDatabase %q -> %q", before.Common.ServiceDatabase.Name, after.Common.ServiceDatabase.Name)
	}

	if len(after.Common.ExtraEnv) != len(before.Common.ExtraEnv) {
		t.Fatalf("extraEnv went from %d entries to %d", len(before.Common.ExtraEnv), len(after.Common.ExtraEnv))
	}
	beforeEnv := indexEnv(before.Common.ExtraEnv)
	for name, want := range beforeEnv {
		got, ok := indexEnv(after.Common.ExtraEnv)[name]
		if !ok {
			t.Errorf("%s disappeared from extraEnv", name)
			continue
		}
		if (want.ValueFrom == nil) != (got.ValueFrom == nil) {
			t.Errorf("%s changed between a literal and a secret reference", name)
			continue
		}
		if want.ValueFrom == nil {
			if want.Value != got.Value {
				t.Errorf("%s value changed", name)
			}
			continue
		}
		if want.ValueFrom.SecretKeyRef.Name != got.ValueFrom.SecretKeyRef.Name ||
			want.ValueFrom.SecretKeyRef.Key != got.ValueFrom.SecretKeyRef.Key {
			t.Errorf("%s points at a different secret: %+v -> %+v", name,
				want.ValueFrom.SecretKeyRef, got.ValueFrom.SecretKeyRef)
		}
		if optionalOf(want) != optionalOf(got) {
			t.Errorf("%s optional %v -> %v: losing the flag turns a disabled capability into a pod that never starts",
				name, optionalOf(want), optionalOf(got))
		}
	}
}

// TestUnadoptedRealAppLosesItself is the control: the same render WITHOUT
// adoption, so the assertions above cannot pass for free. It states the damage
// a console write does to this app today, which is what adoption exists to
// prevent.
func TestUnadoptedRealAppLosesItself(t *testing.T) {
	existing := readFixture(t, "testdata/telemost-bot-values.yaml")

	prev := renderer.PgRouterClusterIP
	renderer.PgRouterClusterIP = "10.96.139.238"
	defer func() { renderer.PgRouterClusterIP = prev }()

	rendered, err := renderer.RenderAppValues(renderer.AppSpec{
		Name:     "telemost-bot",
		Image:    "nexus.dada-tuda.ru/dada/telemost-bot:master-1.0.0-6",
		Port:     8080,
		Replicas: 1,
		Profile:  "small",
	})
	if err != nil {
		t.Fatalf("RenderAppValues: %v", err)
	}
	merged, err := renderer.MergeAppValuesWith(existing, rendered, renderer.MergeOptions{})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	before := parseAppShape(t, existing)
	after := parseAppShape(t, merged)
	if before.Common.ServicePort == after.Common.ServicePort {
		t.Fatalf("an unadopted write kept servicePort at %d, so the adoption test proves nothing", after.Common.ServicePort)
	}
	if equalStringMap(before.Common.Resources.Requests, after.Common.Resources.Requests) &&
		equalStringMap(before.Common.Resources.Limits, after.Common.Resources.Limits) {
		t.Fatalf("an unadopted write kept the resource envelope, so the adoption test proves nothing about it")
	}
}

func readFixture(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

type appShape struct {
	Common struct {
		ServicePort int    `yaml:"servicePort"`
		UseDotEnv   string `yaml:"useDotEnv"`
		Replicas    int    `yaml:"replicas"`
		Image       struct {
			Name string `yaml:"name"`
			Tag  string `yaml:"tag"`
		} `yaml:"image"`
		Resources struct {
			Requests map[string]string `yaml:"requests"`
			Limits   map[string]string `yaml:"limits"`
		} `yaml:"resources"`
		EnvFileValue    map[string]string `yaml:"envFileValue"`
		ServiceDatabase struct {
			Name string `yaml:"name"`
		} `yaml:"serviceDatabase"`
		ExtraEnv []shapeEnvVar `yaml:"extraEnv"`
	} `yaml:"common"`
}

type shapeEnvVar struct {
	Name      string `yaml:"name"`
	Value     string `yaml:"value"`
	ValueFrom *struct {
		SecretKeyRef struct {
			Name     string `yaml:"name"`
			Key      string `yaml:"key"`
			Optional *bool  `yaml:"optional"`
		} `yaml:"secretKeyRef"`
	} `yaml:"valueFrom"`
}

func parseAppShape(t *testing.T, valuesYAML string) appShape {
	t.Helper()
	var out appShape
	if err := yaml.Unmarshal([]byte(valuesYAML), &out); err != nil {
		t.Fatalf("parse values: %v", err)
	}
	return out
}

func indexEnv(list []shapeEnvVar) map[string]shapeEnvVar {
	out := map[string]shapeEnvVar{}
	for _, e := range list {
		out[e.Name] = e
	}
	return out
}

func optionalOf(e shapeEnvVar) bool {
	if e.ValueFrom == nil || e.ValueFrom.SecretKeyRef.Optional == nil {
		return false
	}
	return *e.ValueFrom.SecretKeyRef.Optional
}

func equalStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

package worker

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// TestParseAdoptableValues_ReadsTelemostBotConfig checks the read half of
// adoption against the file that was actually broken: six literal variables,
// two pointing at Secrets the console does not own, a service port of 8000 and
// a .env mount.
func TestParseAdoptableValues_ReadsTelemostBotConfig(t *testing.T) {
	got, err := parseAdoptableValues(handMaintainedValues)
	if err != nil {
		t.Fatalf("parseAdoptableValues: %v", err)
	}
	if got.ServicePort != 8000 {
		t.Fatalf("servicePort = %d, want 8000", got.ServicePort)
	}
	if got.UseDotEnv != "true" {
		t.Fatalf("useDotEnv = %q, want \"true\"", got.UseDotEnv)
	}
	wantPlain := map[string]string{
		"POSTGRES_HOST": "telemost-bot-db",
		"POSTGRES_PORT": "5432",
		"POSTGRES_DB":   "telemost",
		"POSTGRES_USER": "telemost",
		"KEYCLOAK_URL":  "https://auth.dada-tuda.ru",
		"LOG_LEVEL":     "info",
	}
	if len(got.Plain) != len(wantPlain) {
		t.Fatalf("adopted %d literal vars (%v), want %d", len(got.Plain), got.Plain, len(wantPlain))
	}
	for k, v := range wantPlain {
		if got.Plain[k] != v {
			t.Fatalf("adopted %s = %q, want %q", k, got.Plain[k], v)
		}
	}
	wantRefs := []renderer.SecretRefEnvVar{
		{Name: "BOT_TOKEN", SecretName: "telemost-bot-secrets", SecretKey: "bot_token"},
		{Name: "POSTGRES_PASSWORD", SecretName: "telemost-bot-db-credentials", SecretKey: "password"},
	}
	if len(got.Refs) != len(wantRefs) {
		t.Fatalf("adopted %d secret references (%v), want %d", len(got.Refs), got.Refs, len(wantRefs))
	}
	for i, want := range wantRefs {
		if got.Refs[i] != want {
			t.Fatalf("secret reference %d = %+v, want %+v", i, got.Refs[i], want)
		}
	}
	if _, leaked := got.Plain["BOT_TOKEN"]; leaked {
		t.Fatal("a secret reference was adopted as a literal value")
	}
}

// TestAdoptedAppRendersAsItself is the point of the whole feature: once an app
// created outside the console has been adopted, the console's own render
// reproduces it instead of replacing it.
//
// The comparison is against the same render WITHOUT adoption, driven by the one
// thing adoption changes that the merge cannot rescue -- the service port. The
// merge keeps env entries the console never heard of, but servicePort is a key
// the console CLAIMS: an app whose port was ever chosen explicitly gets no
// advisory protection, so an unadopted render moves 8000 to whatever the
// console guessed and the startup probe hits a port nothing listens on. That
// was half of the 2026-08-21 outage.
func TestAdoptedAppRendersAsItself(t *testing.T) {
	adopted, err := parseAdoptableValues(handMaintainedValues)
	if err != nil {
		t.Fatalf("parseAdoptableValues: %v", err)
	}

	env := resolvedEnv{Plain: adopted.Plain, Secret: map[string]string{}, Refs: adopted.Refs}
	spec := renderer.AppSpec{
		Name:     "telemost-bot",
		Image:    "nexus.dada-tuda.ru/dada/telemost-bot:master-1.0.0-42",
		Replicas: 1,
		Port:     adopted.ServicePort,
	}
	env.applyTo(&spec, "telemost-bot")
	rendered, err := renderer.RenderAppValues(spec)
	if err != nil {
		t.Fatalf("RenderAppValues: %v", err)
	}
	merged, err := renderer.MergeAppValuesWith(handMaintainedValues, rendered, renderer.MergeOptions{})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	if dropped := renderer.DroppedPaths(handMaintainedValues, merged); len(dropped) > 0 {
		t.Fatalf("adopted render still drops %v", dropped)
	}
	if port := mergedServicePort(t, merged); port != 8000 {
		t.Fatalf("adopted render moved servicePort to %d, want 8000", port)
	}
	// Asserted on the RENDER, not the merge: merging into the file already in
	// git keeps entries the console never heard of, so a merged file proves
	// nothing about what the console knows. The render is what survives a first
	// write -- a move to another environment, or a values.yaml that does not
	// exist yet -- and that is where an unadopted app loses its credentials.
	assertEnvEntry(t, rendered, "POSTGRES_PASSWORD", "", "telemost-bot-db-credentials", "password")
	assertEnvEntry(t, rendered, "BOT_TOKEN", "", "telemost-bot-secrets", "bot_token")
	assertEnvEntry(t, rendered, "POSTGRES_HOST", "telemost-bot-db", "", "")

	unadopted, err := renderer.RenderAppValues(renderer.AppSpec{
		Name:     "telemost-bot",
		Image:    "nexus.dada-tuda.ru/dada/telemost-bot:master-1.0.0-42",
		Replicas: 1,
		Port:     8080,
	})
	if err != nil {
		t.Fatalf("RenderAppValues: %v", err)
	}
	unadoptedMerged, err := renderer.MergeAppValuesWith(handMaintainedValues, unadopted, renderer.MergeOptions{})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if port := mergedServicePort(t, unadoptedMerged); port != 8080 {
		t.Fatalf("without adoption the port survived as %d, so this test proves nothing", port)
	}
}

// mergedServicePort reads common.servicePort out of a merged values.yaml.
func mergedServicePort(t *testing.T, merged string) int {
	t.Helper()
	var doc struct {
		Common struct {
			ServicePort int `yaml:"servicePort"`
		} `yaml:"common"`
	}
	if err := yaml.Unmarshal([]byte(merged), &doc); err != nil {
		t.Fatalf("parse merged values: %v", err)
	}
	return doc.Common.ServicePort
}

// assertEnvEntry checks one extraEnv entry by name: either its literal value or
// the Secret it points at, whichever the caller names.
func assertEnvEntry(t *testing.T, valuesYAML, name, wantValue, wantSecret, wantKey string) {
	t.Helper()
	var doc struct {
		Common struct {
			ExtraEnv []struct {
				Name      string  `yaml:"name"`
				Value     *string `yaml:"value"`
				ValueFrom *struct {
					SecretKeyRef struct {
						Name string `yaml:"name"`
						Key  string `yaml:"key"`
					} `yaml:"secretKeyRef"`
				} `yaml:"valueFrom"`
			} `yaml:"extraEnv"`
		} `yaml:"common"`
	}
	if err := yaml.Unmarshal([]byte(valuesYAML), &doc); err != nil {
		t.Fatalf("parse values: %v", err)
	}
	for _, e := range doc.Common.ExtraEnv {
		if e.Name != name {
			continue
		}
		if wantSecret != "" {
			if e.ValueFrom == nil {
				t.Fatalf("%s lost its secret reference", name)
			}
			if e.ValueFrom.SecretKeyRef.Name != wantSecret || e.ValueFrom.SecretKeyRef.Key != wantKey {
				t.Fatalf("%s points at secret/%s:%s, want secret/%s:%s", name,
					e.ValueFrom.SecretKeyRef.Name, e.ValueFrom.SecretKeyRef.Key, wantSecret, wantKey)
			}
			return
		}
		if e.Value == nil || *e.Value != wantValue {
			t.Fatalf("%s = %v, want %q", name, e.Value, wantValue)
		}
		return
	}
	t.Fatalf("%s is missing from the values", name)
}

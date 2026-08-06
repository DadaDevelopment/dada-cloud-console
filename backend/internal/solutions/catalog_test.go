package solutions

import (
	"strings"
	"testing"
)

// The catalog is data an operator reads as a promise ("one click and this
// works"), so the invariants that make that promise true are asserted here
// rather than left to review.
func TestV1CatalogInvariants(t *testing.T) {
	seenSlug := map[string]bool{}
	for _, s := range V1 {
		if s.Slug == "" || s.Name == "" || s.Tagline == "" {
			t.Fatalf("solution %q: slug, name and tagline are all required", s.Slug)
		}
		if seenSlug[s.Slug] {
			t.Fatalf("duplicate slug %q", s.Slug)
		}
		seenSlug[s.Slug] = true

		if len(s.Services) == 0 {
			t.Fatalf("solution %q has no services", s.Slug)
		}

		var primaries, roots int
		seenSuffix := map[string]bool{}
		for _, svc := range s.Services {
			if svc.Image == "" {
				t.Fatalf("solution %q service %q has no image", s.Slug, svc.NameSuffix)
			}
			if seenSuffix[svc.NameSuffix] {
				t.Fatalf("solution %q has two services with suffix %q", s.Slug, svc.NameSuffix)
			}
			seenSuffix[svc.NameSuffix] = true
			if svc.NameSuffix == "" {
				roots++
			}
			if svc.Primary {
				primaries++
			}
			// A published port on a service nobody links to is a hole in the VM's
			// firewall with no user-visible purpose.
			if len(svc.Ports) > 0 && !svc.Primary {
				t.Fatalf("solution %q service %q publishes ports but is not primary", s.Slug, svc.NameSuffix)
			}
		}
		if roots != 1 {
			t.Fatalf("solution %q must have exactly one service with an empty suffix, got %d", s.Slug, roots)
		}
		if primaries > 1 {
			t.Fatalf("solution %q has %d primary services, want at most 1", s.Slug, primaries)
		}

		// Every env key the install writes must be unique across params and
		// generated secrets: a collision means one silently wins and the other
		// never reaches the container.
		seenEnv := map[string]string{}
		for _, p := range s.Params {
			if p.Key == "" || p.EnvKey == "" || p.Label == "" {
				t.Fatalf("solution %q param %q: key, env key and label are required", s.Slug, p.Key)
			}
			if owner, dup := seenEnv[p.EnvKey]; dup {
				t.Fatalf("solution %q: env key %q claimed by both %q and %q", s.Slug, p.EnvKey, owner, p.Key)
			}
			seenEnv[p.EnvKey] = p.Key
			if p.Kind == ParamSecret && p.Default != "" {
				t.Fatalf("solution %q param %q: a secret must never ship a default", s.Slug, p.Key)
			}
			if p.Kind == ParamSelect && len(p.Options) == 0 {
				t.Fatalf("solution %q param %q: select needs options", s.Slug, p.Key)
			}
		}
		known := map[string]bool{}
		for _, g := range s.Secrets {
			if g.Key == "" || g.EnvKey == "" {
				t.Fatalf("solution %q generated secret: key and env key are required", s.Slug)
			}
			if owner, dup := seenEnv[g.EnvKey]; dup {
				t.Fatalf("solution %q: env key %q claimed by both %q and %q", s.Slug, g.EnvKey, owner, g.Key)
			}
			seenEnv[g.EnvKey] = g.Key
			if g.Kind != GeneratedPassword && g.Kind != GeneratedSecret {
				t.Fatalf("solution %q secret %q: unknown kind %q", s.Slug, g.Key, g.Kind)
			}
			known[g.Key] = true
		}
		for _, k := range s.RevealKeys {
			if !known[k] {
				t.Fatalf("solution %q reveals %q, which it does not generate", s.Slug, k)
			}
		}
	}
}

// Hermes is the reason this package exists, and two properties of its entry are
// load-bearing rather than cosmetic: the dashboard may only bind non-loopback
// because the install always mints credentials for it, and the agent's data
// volume is shared by both services (the memory IS the product).
func TestHermesDashboardIsNeverExposedWithoutCredentials(t *testing.T) {
	s, ok := Lookup("hermes-agent")
	if !ok {
		t.Fatal("hermes-agent missing from the catalog")
	}

	var dashboard Service
	for _, svc := range s.Services {
		if svc.NameSuffix == "dashboard" {
			dashboard = svc
		}
	}
	if len(dashboard.Ports) == 0 {
		t.Fatal("dashboard publishes no port, so nobody can reach it")
	}
	if !containsArg(dashboard.Command, "0.0.0.0") {
		t.Fatalf("dashboard command %v no longer binds non-loopback; the published port would be dead", dashboard.Command)
	}

	// Upstream fails closed on a non-loopback bind with no auth provider, so the
	// install MUST supply both a username and a password.
	var hasUser bool
	for _, p := range s.Params {
		if p.EnvKey == "HERMES_DASHBOARD_BASIC_AUTH_USERNAME" && p.Required {
			hasUser = true
		}
	}
	if !hasUser {
		t.Fatal("dashboard username is not a required param; the container would fail closed at boot")
	}
	var hasPassword, hasSigningSecret bool
	for _, g := range s.Secrets {
		switch g.EnvKey {
		case "HERMES_DASHBOARD_BASIC_AUTH_PASSWORD":
			hasPassword = true
		case "HERMES_DASHBOARD_BASIC_AUTH_SECRET":
			hasSigningSecret = true
		}
	}
	if !hasPassword {
		t.Fatal("no generated dashboard password; a non-loopback bind would fail closed")
	}
	if !hasSigningSecret {
		t.Fatal("no generated session signing secret; every restart would log the customer out")
	}

	var mounts int
	for _, svc := range s.Services {
		for _, v := range svc.Volumes {
			if strings.HasSuffix(v, ":/opt/data") {
				mounts++
			}
		}
	}
	if mounts != len(s.Services) {
		t.Fatalf("%d of %d hermes services mount the data volume; the agent's memory must be shared and persistent", mounts, len(s.Services))
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if strings.Contains(a, want) {
			return true
		}
	}
	return false
}

func TestPinnedRequiresEveryDigest(t *testing.T) {
	s := Solution{Services: []Service{{Image: "a", Digest: "sha256:1"}, {Image: "b"}}}
	if s.Pinned() {
		t.Fatal("a solution with one unpinned service must not be installable")
	}
	s.Services[1].Digest = "sha256:2"
	if !s.Pinned() {
		t.Fatal("a fully pinned solution must be installable")
	}
	if (Solution{}).Pinned() {
		t.Fatal("a solution with no services is not installable")
	}
}

func TestImageRefAndAppName(t *testing.T) {
	svc := Service{Image: "ghcr.io/x/y:v1"}
	if got := svc.ImageRef(); got != "ghcr.io/x/y:v1" {
		t.Fatalf("unpinned ref = %q", got)
	}
	svc.Digest = "sha256:abc"
	if got := svc.ImageRef(); got != "ghcr.io/x/y:v1@sha256:abc" {
		t.Fatalf("pinned ref = %q", got)
	}
	if got := svc.AppName("hermes"); got != "hermes" {
		t.Fatalf("root app name = %q", got)
	}
	svc.NameSuffix = "dashboard"
	if got := svc.AppName("hermes"); got != "hermes-dashboard" {
		t.Fatalf("suffixed app name = %q", got)
	}
}

func TestValidateInstanceName(t *testing.T) {
	for _, ok := range []string{"hermes", "hermes-2", "a"} {
		if err := ValidateInstanceName(ok); err != nil {
			t.Fatalf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Hermes", "2hermes", "hermes_1", "hermes-", strings.Repeat("a", 41)} {
		if err := ValidateInstanceName(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestResolveParams(t *testing.T) {
	s := Solution{Params: []Param{
		{Key: "url", EnvKey: "URL", Kind: ParamText, Required: true, Default: "https://d"},
		{Key: "key", EnvKey: "KEY", Kind: ParamSecret, Required: true},
		{Key: "mode", EnvKey: "MODE", Kind: ParamSelect, Options: []string{"a", "b"}},
	}}

	env, err := s.ResolveParams(map[string]string{"key": " k "})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if env["URL"] != "https://d" {
		t.Fatalf("default not applied: %q", env["URL"])
	}
	if env["KEY"] != "k" {
		t.Fatalf("value not trimmed: %q", env["KEY"])
	}
	if _, set := env["MODE"]; set {
		t.Fatal("optional param with no value must not be written")
	}

	if _, err := s.ResolveParams(map[string]string{}); err == nil {
		t.Fatal("missing required param accepted")
	}
	if _, err := s.ResolveParams(map[string]string{"key": "k", "nope": "x"}); err == nil {
		t.Fatal("unknown param accepted")
	}
	if _, err := s.ResolveParams(map[string]string{"key": "k", "mode": "c"}); err == nil {
		t.Fatal("value outside the option set accepted")
	}
	// A newline would split one .env line in two and let the tail of a value
	// become an attacker-chosen variable.
	if _, err := s.ResolveParams(map[string]string{"key": "k\nEVIL=1"}); err == nil {
		t.Fatal("newline in a value accepted")
	}
}

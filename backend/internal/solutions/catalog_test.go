package solutions

import (
	"strings"
	"testing"
)

// The catalog is data a customer reads as a promise ("one click and this
// works"), so the invariants that keep the promise true are asserted here
// rather than left to review.
func TestV1CatalogInvariants(t *testing.T) {
	validProfile := map[string]bool{"small": true, "medium": true, "large": true}
	seenSlug := map[string]bool{}
	seenRepo := map[string]bool{}

	for _, s := range V1 {
		if s.Slug == "" || s.Name == "" || s.Tagline == "" || s.About == "" {
			t.Fatalf("solution %q: slug, name, tagline and about are all required", s.Slug)
		}
		if seenSlug[s.Slug] {
			t.Fatalf("duplicate slug %q", s.Slug)
		}
		seenSlug[s.Slug] = true
		// The slug is the default app name, so it has to be a legal one.
		if err := ValidateInstanceName(s.Slug); err != nil {
			t.Fatalf("solution %q: slug is not a usable app name: %v", s.Slug, err)
		}
		if s.Homepage == "" || s.License == "" {
			t.Fatalf("solution %q: a third-party project must name its homepage and license", s.Slug)
		}
		if s.FirstRun == "" {
			t.Fatalf("solution %q: say what to do once it is up", s.Slug)
		}

		if s.IsImage() {
			if s.Repo != "" {
				t.Fatalf("solution %q: an image entry must not also name a repo, or the card lies about which track it took", s.Slug)
			}
			if s.IconRepo == "" {
				t.Fatalf("solution %q: an image entry has no repo to take a logo from, so IconRepo is required", s.Slug)
			}
			if !strings.Contains(s.Image, ":") || strings.HasSuffix(s.Image, ":latest") {
				t.Fatalf("solution %q: image %q must be pinned to a real tag, never latest", s.Slug, s.Image)
			}
			if s.Volume != nil {
				if !strings.HasPrefix(s.Volume.Path, "/") || strings.Contains(s.Volume.Path, "..") {
					t.Fatalf("solution %q: volume path %q must be absolute and free of ..", s.Slug, s.Volume.Path)
				}
				if s.Volume.Size == "" {
					t.Fatalf("solution %q: volume needs a size", s.Slug)
				}
				if s.Volume.FSGroup < 0 || s.Volume.FSGroup > 65535 {
					t.Fatalf("solution %q: fs group %d is not a group id", s.Slug, s.Volume.FSGroup)
				}
			}
		} else {
			// The repository is the whole point of a built entry: it is what gets built.
			if _, err := ParseRepoURL(s.Repo); err != nil {
				t.Fatalf("solution %q: repo %q is not a usable owner/name: %v", s.Slug, s.Repo, err)
			}
			if seenRepo[strings.ToLower(s.Repo)] {
				t.Fatalf("two entries build %q", s.Repo)
			}
			seenRepo[strings.ToLower(s.Repo)] = true
			if s.Branch == "" {
				t.Fatalf("solution %q: branch is required; the default is not the same on every repo", s.Slug)
			}
			if s.RootDir == "" {
				t.Fatalf("solution %q: root dir is required (\".\" for the repository root)", s.Slug)
			}
			if s.Volume != nil {
				t.Fatalf("solution %q: the build track has nowhere to put a volume; make it an image entry", s.Slug)
			}
		}
		if s.Icon() == "" {
			t.Fatalf("solution %q: no logo; the console draws entries as logo chips", s.Slug)
		}
		if s.Category == "" {
			t.Fatalf("solution %q: category is required; the console groups by it", s.Slug)
		}
		if s.Port < 1 || s.Port > 65535 {
			t.Fatalf("solution %q: port %d is not a port; a wrong one deploys green and answers 502", s.Slug, s.Port)
		}
		if !validProfile[s.Profile] {
			t.Fatalf("solution %q: profile %q is not one of small/medium/large", s.Slug, s.Profile)
		}

		for _, p := range s.Params {
			if p.Key == "" || p.EnvKey == "" || p.Label == "" {
				t.Fatalf("solution %q param %q: key, env key and label are required", s.Slug, p.Key)
			}
			if p.Kind == ParamSecret && p.Default != "" {
				t.Fatalf("solution %q param %q: a secret must never ship a default", s.Slug, p.Key)
			}
			if p.Kind == ParamSelect && len(p.Options) == 0 {
				t.Fatalf("solution %q param %q: select needs options", s.Slug, p.Key)
			}
		}
	}
}

// An app created by the build pipeline has no volume: a project that keeps
// state on disk would lose it on every redeploy. So a built entry must be
// stateless and must build from its own root Dockerfile, and anything stateful
// belongs on the image track — this test is what makes crossing the two a
// deliberate act rather than an oversight.
func TestEveryBuiltEntryShipsItsOwnDockerfileBuild(t *testing.T) {
	for _, s := range V1 {
		if s.IsImage() {
			continue
		}
		if s.Framework != "dockerfile" {
			t.Fatalf("solution %q builds via %q; every built entry was verified against its own root Dockerfile", s.Slug, s.Framework)
		}
	}
}

// Every category an entry claims must be one the console knows how to title,
// otherwise the entry lands in a group with no name on it.
func TestEveryCategoryHasATitle(t *testing.T) {
	titled := map[Category]bool{}
	for _, c := range CategoryTitles {
		if titled[c.Category] {
			t.Fatalf("category %q titled twice", c.Category)
		}
		titled[c.Category] = true
	}
	for _, s := range V1 {
		if !titled[s.Category] {
			t.Fatalf("solution %q is in category %q, which nothing titles", s.Slug, s.Category)
		}
	}
}

func TestIsCatalogRepo(t *testing.T) {
	if !IsCatalogRepo("CorentinTh/it-tools") {
		t.Fatal("catalog repo not recognised; its deploys would never be reaped")
	}
	if !IsCatalogRepo("corentinth/IT-TOOLS") {
		t.Fatal("match must be case-insensitive, like GitHub names")
	}
	// A fork is the customer's own work, not a demo we offered.
	if IsCatalogRepo("acme/it-tools") {
		t.Fatal("a fork must not be treated as a catalog demo")
	}
	if IsCatalogRepo("") {
		t.Fatal("empty repo name matched")
	}
}

func TestParseRepoURL(t *testing.T) {
	want := "excalidraw/excalidraw"
	for _, in := range []string{
		"excalidraw/excalidraw",
		"https://github.com/excalidraw/excalidraw",
		"https://github.com/excalidraw/excalidraw/",
		"https://github.com/excalidraw/excalidraw.git",
		"http://www.github.com/excalidraw/excalidraw",
		"git@github.com:excalidraw/excalidraw.git",
		"ssh://git@github.com/excalidraw/excalidraw",
		"  https://github.com/excalidraw/excalidraw  ",
		// A deep link keeps only the repository: branch and subdirectory are
		// choices the form asks for explicitly.
		"https://github.com/excalidraw/excalidraw/tree/master/packages/excalidraw",
	} {
		got, err := ParseRepoURL(in)
		if err != nil {
			t.Fatalf("%q rejected: %v", in, err)
		}
		if got != want {
			t.Fatalf("%q -> %q, want %q", in, got, want)
		}
	}

	for _, bad := range []string{
		"", "   ", "https://gitlab.com/owner/repo", "https://github.com",
		"https://github.com/owner", "not a url", "/leading-slash",
	} {
		if got, err := ParseRepoURL(bad); err == nil {
			t.Fatalf("%q accepted as %q", bad, got)
		}
	}
}

func TestValidateInstanceName(t *testing.T) {
	for _, ok := range []string{"excalidraw", "it-tools", "a"} {
		if err := ValidateInstanceName(ok); err != nil {
			t.Fatalf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Excalidraw", "2fast", "my_app", "app-", strings.Repeat("a", 41)} {
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

// An entry that names no runtime at all is unreachable from every environment,
// which is a card nobody can install.
func TestDeclaredRuntimesAreInstallable(t *testing.T) {
	known := map[Runtime]bool{RuntimeK8s: true, RuntimeVM: true}
	for _, s := range V1 {
		runtimes := s.SupportedRuntimes()
		if len(runtimes) == 0 {
			t.Fatalf("solution %q supports no runtime at all", s.Slug)
		}
		for _, r := range runtimes {
			if !known[r] {
				t.Fatalf("solution %q claims runtime %q", s.Slug, r)
			}
		}
	}
}

func TestSupportedRuntimesDerivation(t *testing.T) {
	for _, s := range []Solution{
		{Slug: "built", Repo: "owner/name"},
		{Slug: "img", Image: "owner/name:1"},
	} {
		if !s.SupportsRuntime(RuntimeVM) || !s.SupportsRuntime(RuntimeK8s) {
			t.Fatalf("%q runs on both substrates by default, got %v", s.Slug, s.SupportedRuntimes())
		}
	}
	vmOnly := Solution{Slug: "game", Image: "owner/name:1", Runtimes: []Runtime{RuntimeVM}}
	if vmOnly.SupportsRuntime(RuntimeK8s) {
		t.Fatalf("a declared runtime list must win over the derivation, got %v", vmOnly.SupportedRuntimes())
	}
}

// A game server is reachable only because the VM publishes its port directly.
// A Kubernetes environment publishes HTTP through the shared ingress and
// nothing else, so installing one there deploys green and never accepts a
// player — the gate exists to say that instead.
func TestGameServersAreVMOnly(t *testing.T) {
	for _, s := range V1 {
		if s.Category != CategoryGames {
			continue
		}
		if s.SupportsRuntime(RuntimeK8s) {
			t.Fatalf("game %q claims Kubernetes, which cannot publish its port", s.Slug)
		}
		if s.Volume == nil {
			t.Fatalf("game %q keeps its world on disk and needs a volume", s.Slug)
		}
	}
}

package solutions

import (
	"strings"
	"testing"
)

// TestResolvePastedLinkOutranksEverything holds the resolver's one hard rule:
// a recognised link is a statement of intent, and no fuzzy catalog match may
// climb over it. When this breaks, the professional pasting a repository URL
// gets someone else's project offered first.
func TestResolvePastedLinkOutranksEverything(t *testing.T) {
	res := Resolve("https://github.com/acme/docs-portal")
	if len(res.Candidates) == 0 {
		t.Fatal("no candidates for a pasted link")
	}
	top := res.Candidates[0]
	if top.Kind != CandidateRepo || top.Repo != "acme/docs-portal" {
		t.Fatalf("top candidate = %s %q, want repo acme/docs-portal", top.Kind, top.Repo)
	}
	if res.SearchQuery != "" {
		t.Fatalf("SearchQuery = %q, want empty: a pasted link is already the answer", res.SearchQuery)
	}
}

// TestResolveCatalogLinkKeepsVerifiedSpec covers the case where the pasted link
// happens to be a catalog repository. The customer must get the entry we
// verified — branch, root dir, port — not a bare repository row that makes the
// pipeline guess all three again.
func TestResolveCatalogLinkKeepsVerifiedSpec(t *testing.T) {
	res := Resolve("https://github.com/excalidraw/excalidraw/tree/master/packages")
	if len(res.Candidates) == 0 {
		t.Fatal("no candidates")
	}
	top := res.Candidates[0]
	if top.Kind != CandidateSolution || top.Slug != "excalidraw" {
		t.Fatalf("top candidate = %s %q, want the excalidraw catalog entry", top.Kind, top.Slug)
	}
	if top.Branch != "master" || top.Port != 80 {
		t.Fatalf("verified spec lost: branch=%q port=%d", top.Branch, top.Port)
	}
}

// TestResolvePostFindsPostgresFirst is the OSS-user scenario from the design
// note, written as a test because it is the one behaviour the owner asked for
// by name: typing "post" offers Postgres before the third keystroke.
func TestResolvePostFindsPostgresFirst(t *testing.T) {
	for _, q := range []string{"post", "postg", "pos", "бд", "база"} {
		res := Resolve(q)
		if len(res.Candidates) == 0 {
			t.Fatalf("%q: no candidates", q)
		}
		top := res.Candidates[0]
		if top.Kind != CandidateManaged || top.Engine != "postgres" {
			t.Fatalf("%q: top candidate = %s/%s, want managed postgres", q, top.Kind, top.Engine)
		}
	}
}

// TestResolveRanksExactAboveSubstring keeps the tiers from collapsing into one
// another: an entry the customer named outright cannot sit below one that
// merely mentions the word somewhere.
func TestResolveRanksExactAboveSubstring(t *testing.T) {
	res := Resolve("devdocs")
	if len(res.Candidates) == 0 {
		t.Fatal("no candidates")
	}
	if got := res.Candidates[0].Slug; got != "devdocs" {
		t.Fatalf("top candidate = %q, want devdocs", got)
	}
	if res.Candidates[0].Score != scoreExact {
		t.Fatalf("score = %d, want scoreExact", res.Candidates[0].Score)
	}
}

// TestResolveAliasFindsRussianQuery is why aliases exist: the catalog is in
// Russian for people who have not met these projects before, and "доска" is
// what such a person types when they want Excalidraw.
func TestResolveAliasFindsRussianQuery(t *testing.T) {
	res := Resolve("доска")
	found := false
	for _, c := range res.Candidates {
		if c.Slug == "excalidraw" {
			found = true
		}
	}
	if !found {
		t.Fatalf("доска did not find excalidraw; got %d candidates", len(res.Candidates))
	}
}

// TestResolveShortQuerySkipsSearch guards the shared rate-limit budget: one and
// two character queries match everything, inform nobody, and would burn the
// cluster's per-minute search allowance on the way to the third keystroke.
func TestResolveShortQuerySkipsSearch(t *testing.T) {
	for _, q := range []string{"n", "n8"} {
		if got := Resolve(q).SearchQuery; got != "" {
			t.Fatalf("%q: SearchQuery = %q, want empty", q, got)
		}
	}
	if got := Resolve("n8n").SearchQuery; got != "n8n" {
		t.Fatalf("SearchQuery = %q, want n8n", got)
	}
}

// TestResolveUnknownWordAsksForSearch is the beginner scenario: nothing local
// matches "immich", so the caller is told to go and ask GitHub rather than
// showing an empty list.
func TestResolveUnknownWordAsksForSearch(t *testing.T) {
	res := Resolve("immich")
	for _, c := range res.Candidates {
		if c.Kind == CandidateSolution {
			t.Fatalf("unexpected catalog match for immich: %q", c.Slug)
		}
	}
	if res.SearchQuery == "" {
		t.Fatal("SearchQuery empty: an unknown word must fall through to search")
	}
}

// TestResolveEmptyQuery keeps the empty field from being an error the console
// has to special-case.
func TestResolveEmptyQuery(t *testing.T) {
	res := Resolve("   ")
	if len(res.Candidates) != 0 || res.SearchQuery != "" {
		t.Fatalf("empty query produced %d candidates / search %q", len(res.Candidates), res.SearchQuery)
	}
}

// TestCatalogAliasesAreLowercase keeps matching honest: matchScore lowercases
// the query but compares aliases as written, so an uppercase alias would be
// dead weight nobody could ever hit.
func TestCatalogAliasesAreLowercase(t *testing.T) {
	check := func(owner string, aliases []string) {
		for _, a := range aliases {
			if a != strings.ToLower(a) {
				t.Errorf("%s: alias %q is not lowercase", owner, a)
			}
			if strings.TrimSpace(a) != a || a == "" {
				t.Errorf("%s: alias %q has stray whitespace", owner, a)
			}
		}
	}
	for _, s := range V1 {
		check(s.Slug, s.Aliases)
	}
	for _, m := range ManagedResources {
		check(m.Slug, m.Aliases)
	}
}

// TestOwnerAvatarUsesOwner pins the icon source. Repository avatars do not
// exist on GitHub; the owner's picture is what every GitHub UI shows, and
// getting the owner half wrong silently renders a broken image everywhere.
func TestOwnerAvatarUsesOwner(t *testing.T) {
	if got := OwnerAvatar("freeCodeCamp/devdocs"); got != "https://github.com/freeCodeCamp.png?size=160" {
		t.Fatalf("OwnerAvatar = %q", got)
	}
	if got := OwnerAvatar("nonsense"); got != "" {
		t.Fatalf("OwnerAvatar(nonsense) = %q, want empty", got)
	}
}

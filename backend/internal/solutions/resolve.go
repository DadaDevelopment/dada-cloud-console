package solutions

import (
	"sort"
	"strings"
)

// CandidateKind is what one row of the resolver's answer list is.
//
// CandidateSolution is a curated catalog entry: verified build spec, one click.
// CandidateRepo is a repository the customer named themselves, by link or by
// owner/name — never a demo, and so never reaped. CandidateManaged is a
// platform resource rather than an application (today a managed database); it
// appears in the same list with the same shape because "поднять postgres" is
// one action to the person asking, and the difference between "resource" and
// "application" is our vocabulary, not theirs.
type CandidateKind string

// CandidateSolution marks a curated catalog entry.
const CandidateSolution CandidateKind = "solution"

// CandidateRepo marks a repository the customer named themselves.
const CandidateRepo CandidateKind = "repo"

// CandidateManaged marks a managed platform resource.
const CandidateManaged CandidateKind = "managed"

// Candidate is one row of the resolver's answer.
//
// Icon is an absolute image URL, or "" when the console should draw its own
// glyph (managed resources); for repositories it is the owner's avatar, which
// is what every GitHub UI shows and costs us no asset pipeline. Engine is set
// only for CandidateManaged and names the resource to create. Score orders the
// list, higher first, ties broken by name.
type Candidate struct {
	Kind    CandidateKind
	Slug    string
	Name    string
	Tagline string
	Icon    string

	Repo      string
	Branch    string
	RootDir   string
	Framework string
	Port      int
	Profile   string

	Engine string

	Score int
}

// Result is what one Resolve call produced locally, plus whether the caller
// should still go and ask GitHub.
//
// SearchQuery is non-empty when the local answer is thin enough that a GitHub
// search is worth its rate-limit budget. Empty means "already answered": a
// pasted link needs no search, and neither does a query too short to mean
// anything.
type Result struct {
	Candidates  []Candidate
	SearchQuery string
}

// scorePastedLink outranks every other tier because pasting a link is an
// unambiguous statement of intent, and a ranking that answers it with a fuzzy
// catalog match is arguing with the customer about what they just typed.
const scorePastedLink = 1000

// scoreExact is a whole-string hit on a slug, name or alias.
const scoreExact = 500

// scorePrefix is what the customer is still in the middle of typing.
const scorePrefix = 300

// scoreAlias is a prefix hit on an alias rather than the entry's own name.
const scoreAlias = 200

// scoreSubstring is the weakest tier: a hit anywhere inside a name or tagline.
const scoreSubstring = 100

// minSearchQuery is the shortest string worth spending a GitHub search on. One
// or two characters match everything and inform nobody, and every such call
// comes out of a per-minute budget shared by the whole cluster.
const minSearchQuery = 3

// OwnerAvatar is the owner's GitHub picture, which GitHub serves for any
// account without an API call or a token.
func OwnerAvatar(repoFullName string) string {
	owner, _, ok := strings.Cut(repoFullName, "/")
	if !ok {
		return ""
	}
	return avatarForOwner(owner)
}

// avatarForOwner is the same URL from a bare GitHub account name, for catalog
// entries that have no repository of their own to derive it from.
func avatarForOwner(owner string) string {
	if owner == "" {
		return ""
	}
	return "https://github.com/" + owner + ".png?size=160"
}

// candidateFor renders a catalog entry as an answer row.
func candidateFor(s Solution) Candidate {
	return Candidate{
		Kind:      CandidateSolution,
		Slug:      s.Slug,
		Name:      s.Name,
		Tagline:   s.Tagline,
		Icon:      s.Icon(),
		Repo:      s.Repo,
		Branch:    s.Branch,
		RootDir:   s.RootDir,
		Framework: s.Framework,
		Port:      s.Port,
		Profile:   s.Profile,
	}
}

// Resolve turns one typed string into a ranked answer list.
//
// The console asks "what do you want to run?" exactly once, and that single
// string has to serve three people who share nothing but the keyboard: the
// beginner who types "n8n" and expects a picture, the person who types "post"
// and means a Postgres, and the professional who pastes a repository URL.
// Modes, tabs and a "choose a type first" step are how that one question ends
// up being asked three times.
//
// Ranking: a recognised link wins outright; then curated catalog entries and
// managed resources by how sure the match is. GitHub search never mixes in
// here — it is appended by the caller, below everything local, because a
// curated entry carries a build spec we verified and a search hit carries
// nothing but a name.
//
// A link to a catalog repository resolves to the CATALOG entry, so the customer
// gets the verified branch, root directory and port instead of our best guess.
//
// Pure and offline: it never calls GitHub. The network half belongs to the
// caller and only runs when Result.SearchQuery is set, which is what stops an
// interactive input from spending the search budget on keystrokes that already
// had a good answer.
func Resolve(query string) Result {
	q := strings.TrimSpace(query)
	if q == "" {
		return Result{Candidates: []Candidate{}}
	}
	lower := strings.ToLower(q)

	out := make([]Candidate, 0, 8)
	claimed := make(map[string]bool, 8)

	if full, err := ParseRepoURL(q); err == nil {
		if s, ok := lookupByRepo(full); ok {
			c := candidateFor(s)
			c.Score = scorePastedLink
			out = append(out, c)
			claimed[s.Slug] = true
		} else {
			owner, name, _ := strings.Cut(full, "/")
			out = append(out, Candidate{
				Kind:    CandidateRepo,
				Slug:    full,
				Name:    name,
				Tagline: owner,
				Icon:    OwnerAvatar(full),
				Repo:    full,
				RootDir: ".",
				Score:   scorePastedLink,
			})
		}
	}

	for _, s := range V1 {
		if claimed[s.Slug] {
			continue
		}
		if score := matchScore(lower, s.Slug, s.Name, s.Aliases, s.Tagline); score > 0 {
			c := candidateFor(s)
			c.Score = score
			out = append(out, c)
		}
	}

	for _, m := range ManagedResources {
		if score := matchScore(lower, m.Slug, m.Name, m.Aliases, m.Tagline); score > 0 {
			out = append(out, Candidate{
				Kind:    CandidateManaged,
				Slug:    m.Slug,
				Name:    m.Name,
				Tagline: m.Tagline,
				Engine:  m.Engine,
				Score:   score,
			})
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Name < out[j].Name
	})

	res := Result{Candidates: out}
	pasted := len(out) > 0 && out[0].Score == scorePastedLink
	if !pasted && len([]rune(lower)) >= minSearchQuery {
		res.SearchQuery = q
	}
	return res
}

// lookupByRepo finds a catalog entry by its repository full name.
func lookupByRepo(repoFullName string) (Solution, bool) {
	if repoFullName == "" {
		return Solution{}, false
	}
	for _, s := range V1 {
		if s.Repo == "" {
			continue
		}
		if strings.EqualFold(s.Repo, repoFullName) {
			return s, true
		}
	}
	return Solution{}, false
}

// matchScore rates one entry against an already-lowercased query.
//
// Prefix beats substring because typing runs left to right: someone three
// characters into "postgres" means Postgres, not the project that happens to
// carry "pos" in the middle of a word. The tagline only ever earns the bottom
// tier — it is prose, and prose matches everything eventually.
func matchScore(lowerQuery, slug, name string, aliases []string, tagline string) int {
	slugL := strings.ToLower(slug)
	nameL := strings.ToLower(name)

	if lowerQuery == slugL || lowerQuery == nameL {
		return scoreExact
	}
	for _, a := range aliases {
		if lowerQuery == strings.ToLower(a) {
			return scoreExact
		}
	}
	if strings.HasPrefix(slugL, lowerQuery) || strings.HasPrefix(nameL, lowerQuery) {
		return scorePrefix
	}
	for _, a := range aliases {
		if strings.HasPrefix(strings.ToLower(a), lowerQuery) {
			return scoreAlias
		}
	}
	if strings.Contains(slugL, lowerQuery) || strings.Contains(nameL, lowerQuery) {
		return scoreSubstring
	}
	if len([]rune(lowerQuery)) >= minSearchQuery && strings.Contains(strings.ToLower(tagline), lowerQuery) {
		return scoreSubstring
	}
	return 0
}

package worker

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

func TestGCDecide(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	mark := time.Hour
	purge := 24 * time.Hour
	old := now.Add(-2 * time.Hour)
	fresh := now.Add(-10 * time.Minute)
	longOrphan := now.Add(-48 * time.Hour)
	recentOrphan := now.Add(-2 * time.Hour)

	cases := []struct {
		name          string
		liveBacked    bool
		gitBacked     bool
		gitVerifiable bool
		phase         string
		lastSynced    time.Time
		orphanedAt    *time.Time
		want          gcAction
	}{
		{"live pod keeps app", true, false, true, "Ready", old, nil, gcNone},
		{"git-backed keeps app even with no pod", false, true, true, "Pending", old, nil, gcNone},
		{"git unverifiable + no pod = leave alone", false, false, false, "Unknown", old, nil, gcNone},
		{"dead but fresh = wait for mark grace", false, false, true, "Unknown", fresh, nil, gcNone},
		{"dead and stale = mark", false, false, true, "Unknown", old, nil, gcMark},
		{"orphaned but recently = wait for purge grace", false, false, true, "Orphaned", old, &recentOrphan, gcNone},
		{"orphaned long enough = purge", false, false, true, "Orphaned", old, &longOrphan, gcPurge},
		{"orphaned with nil stamp never purges", false, false, true, "Orphaned", old, nil, gcNone},
		{"orphaned app came back via pod = clear", true, false, true, "Orphaned", old, &longOrphan, gcClear},
		{"orphaned app came back via git = clear", false, true, true, "Orphaned", old, &longOrphan, gcClear},
		{"orphaned but git unverifiable + no pod = hold", false, false, false, "Orphaned", old, &longOrphan, gcNone},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := gcDecide(c.liveBacked, c.gitBacked, c.gitVerifiable, c.phase,
				c.lastSynced, c.orphanedAt, now, mark, purge)
			if got != c.want {
				t.Fatalf("gcDecide = %v, want %v", got, c.want)
			}
		})
	}
}

// childGCSignals composed with gcDecide must preserve the App sweep's core
// invariant for children: death is only provable when BOTH the repo resolved
// AND the kind's cluster LIST succeeded; any alive signal (parent app, live
// cluster object, git hit) wins regardless.
func TestChildGCSignalsThroughGCDecide(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	mark := time.Hour
	purge := 24 * time.Hour
	old := now.Add(-2 * time.Hour)
	longOrphan := now.Add(-48 * time.Hour)

	cases := []struct {
		name         string
		parentExists bool
		kindListable bool
		clusterLive  bool
		repoResolved bool
		nameInTree   bool
		phase        string
		orphanedAt   *time.Time
		want         gcAction
	}{
		{"dead child (all signals verifiably absent) = mark", false, true, false, true, false, "Pending", nil, gcMark},
		{"parent app keeps child", true, true, false, true, false, "Pending", nil, gcNone},
		{"live cluster object keeps child", false, true, true, true, false, "Pending", nil, gcNone},
		{"git tree hit keeps child", false, true, false, true, true, "Pending", nil, gcNone},
		{"kind LIST failed + no parent = unverifiable, hold", false, false, false, true, false, "Pending", nil, gcNone},
		{"repo unresolvable + not live = unverifiable, hold", false, true, false, false, false, "Pending", nil, gcNone},
		{"kind LIST failed but parent exists = still clears orphan", true, false, false, true, false, "Orphaned", &longOrphan, gcClear},
		{"cluster live but kind was listable = clears orphan", false, true, true, true, false, "Orphaned", &longOrphan, gcClear},
		{"orphaned long + verifiably dead = purge", false, true, false, true, false, "Orphaned", &longOrphan, gcPurge},
		{"orphaned long + kind LIST failed = hold, not purge", false, false, false, true, false, "Orphaned", &longOrphan, gcNone},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			liveBacked, gitBacked, verifiable := childGCSignals(
				c.parentExists, c.kindListable, c.clusterLive, c.repoResolved, c.nameInTree)
			got := gcDecide(liveBacked, gitBacked, verifiable, c.phase, old, c.orphanedAt, now, mark, purge)
			if got != c.want {
				t.Fatalf("childGCSignals→gcDecide = %v, want %v", got, c.want)
			}
		})
	}
}

// The git-backed scan must catch the git-native spelling of a resource, not
// just the snapshot name: a PublicApi/Ingress snapshot is named after the
// DASHED fqdn while git manifests carry the dotted form.
func TestTreeContentAndTerms(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "apps", "nextjs-fhvx20")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	resources := "manifests:\n  - kind: PublicApi\n    fqdn: nextjs-fhvx20-406da2.dada-tuda.ru\n"
	if err := os.WriteFile(filepath.Join(appDir, "resources.values.yaml"), []byte(resources), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := map[string]string{}
	content := treeContent(root, cache)

	dashedOnly := []string{"nextjs-fhvx20-406da2-dada-tuda-ru"}
	if anyTermIn(content, dashedOnly) {
		t.Fatal("dashed snapshot name must NOT match dotted git manifest")
	}
	withFqdn := []string{"nextjs-fhvx20-406da2-dada-tuda-ru", "nextjs-fhvx20-406da2.dada-tuda.ru"}
	if !anyTermIn(content, withFqdn) {
		t.Fatal("dotted fqdn term must match the git manifest")
	}
	if anyTermIn(content, []string{""}) {
		t.Fatal("empty term must never match")
	}

	if _, ok := cache[root]; !ok {
		t.Fatal("treeContent must cache per root")
	}
	if got := treeContent(filepath.Join(root, "missing-env"), cache); got != "" {
		t.Fatalf("missing root must yield empty content, got %q", got)
	}
}

// TestAppGitExistsElsewhere is the falsification case for the mismatch that
// destroyed the platform inventory on 2026-08-08: the snapshot row said project
// "platform", the manifest sat under project "delivery", and the exact-path
// probe reported "deleted from git". The row's own path must never count as
// "elsewhere", or every healthy app would be reported as misfiled.
func TestAppGitExistsElsewhere(t *testing.T) {
	mgr := git.New(git.RepoConfig{
		RepoURL:   "https://example.com/dadadevelopment/argo-infra.git",
		Branch:    "live",
		LocalBase: t.TempDir(),
	})

	write := func(rel string) {
		full := filepath.Join(mgr.LocalPath(), rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte("kind: App\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	write(renderer.AppGitPath("delivery", "prod", "jenkins"))
	write(renderer.AppGitPath("platform", "prod", "keycloak"))

	if appGitExists(mgr, "platform", "prod", "jenkins") {
		t.Fatal("precondition: jenkins must be absent at the platform path")
	}

	where, ok := appGitExistsElsewhere(mgr, "platform", "prod", "jenkins")
	if !ok {
		t.Fatal("misfiled manifest under another project must be found")
	}
	if where != renderer.AppGitPath("delivery", "prod", "jenkins") {
		t.Fatalf("wrong path reported: %s", where)
	}

	if _, ok := appGitExistsElsewhere(mgr, "platform", "prod", "keycloak"); ok {
		t.Fatal("an app's own manifest must not count as living elsewhere")
	}

	if _, ok := appGitExistsElsewhere(mgr, "platform", "prod", "never-existed"); ok {
		t.Fatal("a genuinely deleted app must stay deletable")
	}
}

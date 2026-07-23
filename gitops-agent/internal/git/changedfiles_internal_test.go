package git

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// TestChangedFiles_TracksDeletions is the regression guard for the ghost-project
// respawn bug: a DeleteProject git-rm's clusters/<cluster>/projects/<slug>/**,
// and changedFiles must report those removed paths as deletions so the git
// watcher can skip them instead of auto-recreating the just-deleted project.
func TestChangedFiles_TracksDeletions(t *testing.T) {
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}

	projectYAML := "clusters/beget-prod/projects/foo/project.yaml"
	appYAML := "clusters/beget-prod/projects/foo/environments/prod/apps/web/app.yaml"
	keepYAML := "clusters/beget-prod/projects/bar/project.yaml"

	write := func(rel, content string) {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		if _, err := wt.Add(rel); err != nil {
			t.Fatalf("add %s: %v", rel, err)
		}
	}
	commit := func(msg string) plumbing.Hash {
		h, err := wt.Commit(msg, &gogit.CommitOptions{
			Author: &object.Signature{Name: "t", Email: "t@x", When: time.Now()},
		})
		if err != nil {
			t.Fatalf("commit %q: %v", msg, err)
		}
		return h
	}

	write(projectYAML, "project: foo\n")
	write(appYAML, "name: web\n")
	write(keepYAML, "project: bar\n")
	commit("add foo + bar")

	if _, err := wt.Remove("clusters/beget-prod/projects/foo/project.yaml"); err != nil {
		t.Fatalf("remove project.yaml: %v", err)
	}
	if _, err := wt.Remove("clusters/beget-prod/projects/foo/environments/prod/apps/web/app.yaml"); err != nil {
		t.Fatalf("remove app.yaml: %v", err)
	}
	write(keepYAML, "project: bar\ndisplayName: Bar\n")
	delHash := commit("delete project foo, modify bar")

	delCommit, err := repo.CommitObject(delHash)
	if err != nil {
		t.Fatalf("commit object: %v", err)
	}

	files, deleted, err := changedFiles(delCommit)
	if err != nil {
		t.Fatalf("changedFiles: %v", err)
	}

	inFiles := map[string]bool{}
	for _, f := range files {
		inFiles[f] = true
	}
	for _, p := range []string{projectYAML, appYAML, keepYAML} {
		if !inFiles[p] {
			t.Errorf("changedFiles omitted changed path %q; got %v", p, files)
		}
	}

	if !deleted[projectYAML] {
		t.Errorf("deleted set missing removed project.yaml %q; got %v", projectYAML, deleted)
	}
	if !deleted[appYAML] {
		t.Errorf("deleted set missing removed app.yaml %q; got %v", appYAML, deleted)
	}
	if deleted[keepYAML] {
		t.Errorf("modified path %q wrongly flagged as deleted", keepYAML)
	}
}

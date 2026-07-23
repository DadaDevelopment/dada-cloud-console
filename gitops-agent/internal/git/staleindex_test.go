package git_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
)

const staleTestBranch = "master"

func seedRemote(t *testing.T) string {
	t.Helper()

	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	if _, err := gogit.PlainInit(remoteDir, true); err != nil {
		t.Fatalf("init bare remote: %v", err)
	}

	seedDir := filepath.Join(t.TempDir(), "seed")
	seedRepo, err := gogit.PlainInit(seedDir, false)
	if err != nil {
		t.Fatalf("init seed repo: %v", err)
	}
	wt, err := seedRepo.Worktree()
	if err != nil {
		t.Fatalf("seed worktree: %v", err)
	}
	writeAndAdd(t, seedDir, wt, "victim.yaml", "victim: original\n")
	if _, err := wt.Commit("seed", &gogit.CommitOptions{
		Author: &object.Signature{Name: "seed", Email: "seed@test", When: time.Now()},
	}); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
	if _, err := seedRepo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteDir},
	}); err != nil {
		t.Fatalf("create remote: %v", err)
	}
	if err := seedRepo.Push(&gogit.PushOptions{
		RemoteName: "origin",
		RefSpecs: []config.RefSpec{
			config.RefSpec("refs/heads/" + staleTestBranch + ":refs/heads/" + staleTestBranch),
		},
	}); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	return remoteDir
}

func writeAndAdd(t *testing.T, dir string, wt *gogit.Worktree, path, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if _, err := wt.Add(path); err != nil {
		t.Fatalf("add %s: %v", path, err)
	}
}

func remoteFileContent(t *testing.T, remoteDir, path string) string {
	t.Helper()
	repo, err := gogit.PlainOpen(remoteDir)
	if err != nil {
		t.Fatalf("open remote: %v", err)
	}
	ref, err := repo.Reference(plumbing.NewBranchReferenceName(staleTestBranch), true)
	if err != nil {
		t.Fatalf("remote branch ref: %v", err)
	}
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		t.Fatalf("remote head commit: %v", err)
	}
	file, err := commit.File(path)
	if err != nil {
		t.Fatalf("remote file %s: %v", path, err)
	}
	content, err := file.Contents()
	if err != nil {
		t.Fatalf("remote file contents %s: %v", path, err)
	}
	return content
}

func TestCommitFilesAndPush_StaleStagedEditDoesNotRideAlong(t *testing.T) {
	remoteDir := seedRemote(t)

	mgr := git.New(git.RepoConfig{
		RepoURL:   remoteDir,
		Branch:    staleTestBranch,
		LocalBase: t.TempDir(),
	})
	if err := mgr.EnsureCloned(); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}

	cloneRepo, err := gogit.PlainOpen(mgr.LocalPath())
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	cloneWt, err := cloneRepo.Worktree()
	if err != nil {
		t.Fatalf("clone worktree: %v", err)
	}
	writeAndAdd(t, mgr.LocalPath(), cloneWt, "victim.yaml", "victim: CLOBBERED\n")

	sha, err := mgr.CommitFilesAndPush(
		[]git.FileChange{{Path: "target.yaml", Content: "target: new\n"}},
		"add target", "test", "test@test")
	if err != nil {
		t.Fatalf("CommitFilesAndPush: %v", err)
	}

	if got := remoteFileContent(t, remoteDir, "victim.yaml"); got != "victim: original\n" {
		t.Errorf("victim.yaml on remote = %q, want untouched original", got)
	}
	if got := remoteFileContent(t, remoteDir, "target.yaml"); got != "target: new\n" {
		t.Errorf("target.yaml on remote = %q, want %q", got, "target: new\n")
	}

	commit, err := cloneRepo.CommitObject(plumbing.NewHash(sha))
	if err != nil {
		t.Fatalf("pushed commit object: %v", err)
	}
	parent, err := commit.Parents().Next()
	if err != nil {
		t.Fatalf("pushed commit parent: %v", err)
	}
	patch, err := parent.Patch(commit)
	if err != nil {
		t.Fatalf("pushed commit patch: %v", err)
	}
	for _, fp := range patch.FilePatches() {
		from, to := fp.Files()
		name := ""
		if to != nil {
			name = to.Path()
		} else if from != nil {
			name = from.Path()
		}
		if name != "target.yaml" {
			t.Errorf("pushed commit touches unexpected file %q — stale index rode along", name)
		}
	}
}

func TestCommitFilesAndPush_StaleUnstagedEditDoesNotBlockOrLeak(t *testing.T) {
	remoteDir := seedRemote(t)

	mgr := git.New(git.RepoConfig{
		RepoURL:   remoteDir,
		Branch:    staleTestBranch,
		LocalBase: t.TempDir(),
	})
	if err := mgr.EnsureCloned(); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}

	if err := os.WriteFile(filepath.Join(mgr.LocalPath(), "victim.yaml"), []byte("victim: dirty\n"), 0o644); err != nil {
		t.Fatalf("dirty write: %v", err)
	}

	if _, err := mgr.CommitFilesAndPush(
		[]git.FileChange{{Path: "target.yaml", Content: "target: v2\n"}},
		"add target", "test", "test@test"); err != nil {
		t.Fatalf("CommitFilesAndPush with dirty worktree: %v", err)
	}

	if got := remoteFileContent(t, remoteDir, "victim.yaml"); got != "victim: original\n" {
		t.Errorf("victim.yaml on remote = %q, want untouched original", got)
	}
}

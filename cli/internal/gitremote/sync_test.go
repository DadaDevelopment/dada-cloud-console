package gitremote

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestBranchOnOriginSeesOnlyWhatTheRemoteHas is the gate for the git path:
// the platform clones origin, so a branch that exists only locally cannot be
// built no matter how clean the working tree looks.
func TestBranchOnOriginSeesOnlyWhatTheRemoteHas(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	remote := t.TempDir()
	run(t, remote, "init", "--bare")
	dir := t.TempDir()
	run(t, dir, "init")
	run(t, dir, "config", "user.email", "t@example.com")
	run(t, dir, "config", "user.name", "t")
	run(t, dir, "remote", "add", "origin", remote)
	if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte("print(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-m", "first")

	branch, err := git(dir, "branch", "--show-current")
	if err != nil {
		t.Fatal(err)
	}
	if BranchOnOrigin(dir, branch) {
		t.Fatal("branch that was never pushed must not count as buildable")
	}
	run(t, dir, "push", "origin", branch)
	if !BranchOnOrigin(dir, branch) {
		t.Fatal("pushed branch must be buildable")
	}
	if BranchOnOrigin(dir, "") {
		t.Fatal("empty branch name must never look buildable")
	}
}

// TestDirtyTreeStillCountsAsBuildable pins the owner's rule: local edits do
// not knock the deploy off the git road, the remote is still there to build.
func TestDirtyTreeStillCountsAsBuildable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	remote := t.TempDir()
	run(t, remote, "init", "--bare")
	dir := t.TempDir()
	run(t, dir, "init")
	run(t, dir, "config", "user.email", "t@example.com")
	run(t, dir, "config", "user.name", "t")
	run(t, dir, "remote", "add", "origin", remote)
	if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte("print(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-m", "first")
	branch, err := git(dir, "branch", "--show-current")
	if err != nil {
		t.Fatal(err)
	}
	run(t, dir, "push", "origin", branch)
	if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte("print(2)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !BranchOnOrigin(dir, branch) {
		t.Fatal("uncommitted local edits must not hide the pushed branch")
	}
}

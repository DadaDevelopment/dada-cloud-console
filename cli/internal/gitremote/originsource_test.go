package gitremote

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// pushedRepo makes a repository whose origin holds exactly the files named in
// committed, while everything in untracked exists only on disk.
func pushedRepo(t *testing.T, committed, untracked []string) (dir, branch string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	remote := t.TempDir()
	run(t, remote, "init", "--bare")
	dir = t.TempDir()
	run(t, dir, "init")
	run(t, dir, "config", "user.email", "t@example.com")
	run(t, dir, "config", "user.name", "t")
	run(t, dir, "remote", "add", "origin", remote)

	write := func(name string) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range committed {
		write(name)
	}
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-m", "first")
	branch, err := git(dir, "branch", "--show-current")
	if err != nil {
		t.Fatal(err)
	}
	run(t, dir, "push", "origin", branch)
	for _, name := range untracked {
		write(name)
	}
	return dir, branch
}

// TestOriginWithOnlyADocumentFileHasNoSource is the regression lock for the
// deploy of 2026-08-13 13:52: the folder was full of Python, origin/main held
// README.md alone, and ddc queued a build that Jenkins could only kill with
// "framework <empty> has no template and repo ships no Dockerfile".
func TestOriginWithOnlyADocumentFileHasNoSource(t *testing.T) {
	dir, branch := pushedRepo(t,
		[]string{"README.md"},
		[]string{"agent.py", "serve.py", "ui/index.html"})

	if OriginHasSource(dir, branch, "") {
		t.Fatal("a README-only remote must not be treated as buildable")
	}
}

// TestOriginIgnoresTheOtherFilesThatDescribeAProject keeps the rule from
// passing on paperwork: licences, ignore files and a docs tree are not code.
func TestOriginIgnoresTheOtherFilesThatDescribeAProject(t *testing.T) {
	dir, branch := pushedRepo(t,
		[]string{"README.md", "LICENSE", ".gitignore", "docs/design.md", ".github/workflows/ci.yml"},
		nil)

	if OriginHasSource(dir, branch, "") {
		t.Fatal("documentation and repo paperwork must not read as source")
	}
}

// TestOriginWithCommittedCodeHasSource is the other half: once the code is
// pushed, the git path must stay open - including for a file type the CLI
// knows nothing about, since detection is the platform's job, not the CLI's.
func TestOriginWithCommittedCodeHasSource(t *testing.T) {
	dir, branch := pushedRepo(t, []string{"README.md", "main.rb"}, nil)

	if !OriginHasSource(dir, branch, "") {
		t.Fatal("a remote holding code must be buildable")
	}
}

// TestOriginSourceIsJudgedInsideTheAppSubdirectory keeps a monorepo honest:
// the platform builds root_dir, so code sitting elsewhere in the tree does not
// make this app's directory buildable.
func TestOriginSourceIsJudgedInsideTheAppSubdirectory(t *testing.T) {
	dir, branch := pushedRepo(t, []string{"other/main.go", "apps/api/README.md"}, nil)

	if OriginHasSource(dir, branch, "apps/api") {
		t.Fatal("code outside the app's root_dir must not count for it")
	}
	if !OriginHasSource(dir, branch, "other") {
		t.Fatal("the directory that does hold code must still be buildable")
	}
}

// TestUnreadableOriginKeepsTheGitPath makes the check fail open: if git cannot
// answer (no such ref, a shallow clone), the platform is still the authority
// on what it can build, and the CLI must not veto the git path on a guess.
func TestUnreadableOriginKeepsTheGitPath(t *testing.T) {
	dir, _ := pushedRepo(t, []string{"main.go"}, nil)

	if !OriginHasSource(dir, "no-such-branch", "") {
		t.Fatal("an unanswerable check must not block the git path")
	}
	if OriginHasSource(dir, "", "") {
		t.Fatal("an empty branch name has nothing to build")
	}
}

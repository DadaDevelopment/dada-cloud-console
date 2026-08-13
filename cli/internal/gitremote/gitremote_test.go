package gitremote

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseRemote(t *testing.T) {
	cases := []struct {
		in       string
		host     string
		fullName string
		ok       bool
	}{
		{"git@github.com:dada-tuda/console.git", "github.com", "dada-tuda/console", true},
		{"ssh:" + "/" + "/git@github.com/dada-tuda/console.git", "github.com", "dada-tuda/console", true},
		{"https:" + "/" + "/github.com/dada-tuda/console.git", "github.com", "dada-tuda/console", true},
		{"https:" + "/" + "/github.com/dada-tuda/console", "github.com", "dada-tuda/console", true},
		{"https:" + "/" + "/token@gitlab.com/group/sub/app.git", "gitlab.com", "group/sub/app", true},
		{"", "", "", false},
		{"not-a-remote", "", "", false},
	}
	for _, c := range cases {
		host, fullName, ok := ParseRemote(c.in)
		if ok != c.ok || host != c.host || fullName != c.fullName {
			t.Errorf("ParseRemote(%q) = %q, %q, %v; want %q, %q, %v",
				c.in, host, fullName, ok, c.host, c.fullName, c.ok)
		}
	}
}

func TestDetectNonRepo(t *testing.T) {
	dir := t.TempDir()
	if info := Detect(dir); info.IsRepo {
		t.Fatalf("temp dir reported as a repo: %+v", info)
	}
}

func TestDetectRepoWithoutOrigin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run(t, dir, "init")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	info := Detect(dir)
	if !info.IsRepo {
		t.Fatal("initialized repo not detected")
	}
	if info.Unsupported == "" {
		t.Fatal("repo without an origin remote should be unsupported")
	}
}

func TestDetectNonGithubRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run(t, dir, "init")
	run(t, dir, "remote", "add", "origin", "git@bitbucket.org:team/app.git")

	info := Detect(dir)
	if info.Host != "bitbucket.org" || info.FullName != "team/app" {
		t.Fatalf("remote parsed wrong: %+v", info)
	}
	if info.Unsupported == "" {
		t.Fatal("non-github remote should be unsupported")
	}
}

func TestDetectDirtyGithubRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run(t, dir, "init")
	run(t, dir, "remote", "add", "origin", "git@github.com:team/app.git")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", ".")
	run(t, dir, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-m", "first")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	info := Detect(dir)
	if info.Unsupported != "" {
		t.Fatalf("github remote should be supported: %q", info.Unsupported)
	}
	if !info.Dirty {
		t.Fatal("uncommitted change not reported as dirty")
	}
	if info.HeadPushed {
		t.Fatal("repo with no origin/<branch> ref must not report HeadPushed")
	}
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

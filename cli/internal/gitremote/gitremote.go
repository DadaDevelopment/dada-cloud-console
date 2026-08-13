// Package gitremote inspects the working directory's git repository to decide
// whether the console can build the app straight from its remote instead of
// from an uploaded archive.
package gitremote

import (
	"os/exec"
	"strings"
)

// Info describes the git state of a directory. Unsupported carries the human
// reason the git path cannot be used, so the caller can say it out loud
// before falling back to the archive upload.
type Info struct {
	IsRepo       bool
	Host         string
	FullName     string
	Branch       string
	Dirty        bool
	HeadPushed   bool
	Unsupported  string
	RemoteURL    string
	SubdirOfRoot string
}

// ParseRemote splits a remote URL into host and "owner/repo", accepting both
// the ssh and https forms git reports for the same repository.
func ParseRemote(raw string) (host, fullName string, ok bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "", false
	}
	schemeSep := ":" + "/" + "/"
	if i := strings.Index(s, schemeSep); i >= 0 {
		s = s[i+len(schemeSep):]
	}
	if at := strings.Index(s, "@"); at >= 0 {
		s = s[at+1:]
	}
	i := strings.IndexAny(s, ":/")
	if i <= 0 {
		return "", "", false
	}
	host = s[:i]
	path := strings.Trim(s[i+1:], "/")
	path = strings.TrimSuffix(path, ".git")
	if host == "" || !strings.Contains(path, "/") {
		return "", "", false
	}
	return host, path, true
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// Detect reads the repository state of dir. Every failure degrades to "not a
// usable repo" rather than an error, because falling back to the archive path
// is always a valid answer.
func Detect(dir string) Info {
	info := Info{}
	root, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return info
	}
	info.IsRepo = true

	remote, err := git(dir, "remote", "get-url", "origin")
	if err != nil || remote == "" {
		info.Unsupported = "this repo has no 'origin' remote"
		return info
	}
	info.RemoteURL = remote

	host, fullName, ok := ParseRemote(remote)
	if !ok {
		info.Unsupported = "could not read the 'origin' remote URL"
		return info
	}
	info.Host, info.FullName = host, fullName
	if host != "github.com" {
		info.Unsupported = "the console builds from GitHub remotes; yours is " + host
		return info
	}

	branch, err := git(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || branch == "" || branch == "HEAD" {
		info.Unsupported = "this repo has no branch checked out"
		return info
	}
	info.Branch = branch

	if status, err := git(dir, "status", "--porcelain"); err == nil && status != "" {
		info.Dirty = true
	}

	local, lerr := git(dir, "rev-parse", "HEAD")
	remoteHead, rerr := git(dir, "rev-parse", "origin/"+branch)
	info.HeadPushed = lerr == nil && rerr == nil && local != "" && local == remoteHead

	if rel, err := git(dir, "rev-parse", "--show-prefix"); err == nil {
		info.SubdirOfRoot = strings.TrimSuffix(rel, "/")
	}
	return info
}

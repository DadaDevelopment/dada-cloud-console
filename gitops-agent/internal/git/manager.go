package git

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/rs/zerolog/log"
)

// RepoConfig holds credentials for a specific remote repository.
type RepoConfig struct {
	RepoURL   string
	Branch    string
	Username  string
	Token     string
	LocalBase string // root directory; repo cloned into LocalBase/<slug>
}

// Commit is a minimal representation of a git commit for the Git Watcher.
type Commit struct {
	SHA     string
	Message string
	Author  string
	Email   string
	When    time.Time
	// Files changed in this commit (paths relative to repo root).
	Files []string
}

// Manager owns a local clone of one remote repository.
// It serialises all git operations with a mutex so the DB Watcher
// and Git Watcher can share the same manager safely.
type Manager struct {
	cfg  RepoConfig
	path string // absolute path to the local clone
	mu   sync.Mutex
}

// New returns a Manager. The repo is cloned on first use via EnsureCloned.
func New(cfg RepoConfig) *Manager {
	slug := repoSlug(cfg.RepoURL)
	path := filepath.Join(cfg.LocalBase, slug)
	return &Manager{cfg: cfg, path: path}
}

// LocalPath returns the absolute path to the local clone.
func (m *Manager) LocalPath() string { return m.path }

// RepoURL returns the remote URL.
func (m *Manager) RepoURL() string { return m.cfg.RepoURL }

// Branch returns the tracked branch name.
func (m *Manager) Branch() string { return m.cfg.Branch }

// EnsureCloned clones the repo if the local path does not exist yet.
func (m *Manager) EnsureCloned() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := os.Stat(filepath.Join(m.path, ".git")); err == nil {
		return nil // already cloned
	}

	log.Info().Str("repo", m.cfg.RepoURL).Str("branch", m.cfg.Branch).Msg("cloning repo")
	_, err := gogit.PlainClone(m.path, false, &gogit.CloneOptions{
		URL:           m.cfg.RepoURL,
		Auth:          m.auth(),
		ReferenceName: plumbing.NewBranchReferenceName(m.cfg.Branch),
		SingleBranch:  true,
		Depth:         0, // full clone so we can walk history
	})
	if err != nil {
		return fmt.Errorf("cloning %s: %w", m.cfg.RepoURL, err)
	}
	return nil
}

// Pull fetches and fast-forwards the local branch.
// Returns (remoteHEAD, error).
func (m *Manager) Pull() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pull()
}

func (m *Manager) pull() (string, error) {
	repo, err := gogit.PlainOpen(m.path)
	if err != nil {
		return "", fmt.Errorf("opening repo: %w", err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return "", err
	}

	err = wt.Pull(&gogit.PullOptions{
		Auth:          m.auth(),
		ReferenceName: plumbing.NewBranchReferenceName(m.cfg.Branch),
		Force:         false,
	})
	if err != nil && err != gogit.NoErrAlreadyUpToDate {
		return "", fmt.Errorf("pulling: %w", err)
	}

	head, err := repo.Head()
	if err != nil {
		return "", err
	}
	return head.Hash().String(), nil
}

// FileChange is one file write in a git commit.
type FileChange struct {
	Path    string
	Content string
}

// CommitAndPush writes content to relativePath, commits, and pushes.
// On push rejection (non-fast-forward) it pulls with rebase and retries once.
// Returns the commit SHA.
func (m *Manager) CommitAndPush(relativePath, content, commitMessage, authorName, authorEmail string) (string, error) {
	return m.CommitFilesAndPush([]FileChange{{Path: relativePath, Content: content}}, commitMessage, authorName, authorEmail)
}

// CommitFilesAndPush writes one or more files, commits, and pushes.
// On push rejection (non-fast-forward) it pulls with rebase and retries once.
// Returns the commit SHA.
func (m *Manager) CommitFilesAndPush(files []FileChange, commitMessage, authorName, authorEmail string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sha, err := m.writeFilesCommitPush(files, commitMessage, authorName, authorEmail)
	if err == nil {
		return sha, nil
	}

	// Non-fast-forward: rebase on top of remote and retry once.
	if isNonFastForward(err) {
		log.Warn().Int("files", len(files)).Msg("push rejected, rebasing and retrying")
		if _, pullErr := m.pull(); pullErr != nil {
			return "", fmt.Errorf("rebase pull failed: %w (original push error: %w)", pullErr, err)
		}
		return m.writeFilesCommitPush(files, commitMessage, authorName, authorEmail)
	}

	return "", err
}

func (m *Manager) writeFilesCommitPush(files []FileChange, commitMessage, authorName, authorEmail string) (string, error) {
	repo, err := gogit.PlainOpen(m.path)
	if err != nil {
		return "", fmt.Errorf("opening repo: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", err
	}

	for _, file := range files {
		absPath := filepath.Join(m.path, file.Path)
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return "", fmt.Errorf("mkdir %s: %w", file.Path, err)
		}
		if err := os.WriteFile(absPath, []byte(file.Content), 0o644); err != nil {
			return "", fmt.Errorf("writing file %s: %w", file.Path, err)
		}
		if _, err := wt.Add(file.Path); err != nil {
			return "", fmt.Errorf("git add %s: %w", file.Path, err)
		}
	}

	hash, err := wt.Commit(commitMessage, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  authorName,
			Email: authorEmail,
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("git commit: %w", err)
	}

	if err := repo.Push(&gogit.PushOptions{
		Auth:       m.auth(),
		RemoteName: "origin",
		RefSpecs: []config.RefSpec{
			config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", m.cfg.Branch, m.cfg.Branch)),
		},
	}); err != nil {
		return "", fmt.Errorf("git push: %w", err)
	}

	return hash.String(), nil
}

// RemoteHEAD returns the current remote HEAD SHA without modifying the local clone.
func (m *Manager) RemoteHEAD() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	repo, err := gogit.PlainOpen(m.path)
	if err != nil {
		return "", err
	}

	if err := repo.Fetch(&gogit.FetchOptions{
		Auth:       m.auth(),
		RemoteName: "origin",
		RefSpecs: []config.RefSpec{
			config.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", m.cfg.Branch, m.cfg.Branch)),
		},
		Force: true,
	}); err != nil && err != gogit.NoErrAlreadyUpToDate {
		return "", fmt.Errorf("fetch: %w", err)
	}

	ref, err := repo.Reference(
		plumbing.NewRemoteReferenceName("origin", m.cfg.Branch), true)
	if err != nil {
		return "", fmt.Errorf("resolving remote HEAD: %w", err)
	}
	return ref.Hash().String(), nil
}

// CommitsSince returns commits reachable from HEAD that are not reachable from
// fromSHA, in chronological order (oldest first). Each Commit includes the
// list of file paths changed in that commit.
func (m *Manager) CommitsSince(fromSHA string) ([]Commit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// First pull so we see the latest remote state locally.
	if _, err := m.pull(); err != nil {
		return nil, err
	}

	repo, err := gogit.PlainOpen(m.path)
	if err != nil {
		return nil, err
	}

	head, err := repo.Head()
	if err != nil {
		return nil, err
	}

	logOpts := &gogit.LogOptions{From: head.Hash()}
	iter, err := repo.Log(logOpts)
	if err != nil {
		return nil, err
	}

	var commits []Commit
	err = iter.ForEach(func(c *object.Commit) error {
		if c.Hash.String() == fromSHA {
			return fmt.Errorf("stop") // sentinel to stop iteration
		}

		files, ferr := changedFiles(c)
		if ferr != nil {
			return ferr
		}

		commits = append(commits, Commit{
			SHA:     c.Hash.String(),
			Message: c.Message,
			Author:  c.Author.Name,
			Email:   c.Author.Email,
			When:    c.Author.When,
			Files:   files,
		})
		return nil
	})
	if err != nil && err.Error() != "stop" {
		return nil, err
	}

	// Reverse so oldest commit is first.
	for i, j := 0, len(commits)-1; i < j; i, j = i+1, j-1 {
		commits[i], commits[j] = commits[j], commits[i]
	}
	return commits, nil
}

// ReadFile returns the content of a file in the current worktree.
func (m *Manager) ReadFile(relativePath string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := os.ReadFile(filepath.Join(m.path, relativePath))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// RemoveAndPush deletes one or more files (relative paths), commits, and pushes.
// Missing files are skipped silently. Returns the new commit SHA, or "" if
// nothing was actually removed.
func (m *Manager) RemoveAndPush(relativePaths []string, commitMessage, authorName, authorEmail string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.pull(); err != nil {
		return "", err
	}
	repo, err := gogit.PlainOpen(m.path)
	if err != nil {
		return "", fmt.Errorf("opening repo: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	removed := 0
	for _, rel := range relativePaths {
		abs := filepath.Join(m.path, rel)
		if _, err := os.Stat(abs); os.IsNotExist(err) {
			continue
		}
		if _, err := wt.Remove(rel); err != nil {
			return "", fmt.Errorf("git rm %s: %w", rel, err)
		}
		removed++
	}
	if removed == 0 {
		return "", nil
	}
	hash, err := wt.Commit(commitMessage, &gogit.CommitOptions{
		Author: &object.Signature{Name: authorName, Email: authorEmail, When: time.Now()},
	})
	if err != nil {
		return "", fmt.Errorf("git commit (remove): %w", err)
	}
	if err := repo.Push(&gogit.PushOptions{
		Auth:       m.auth(),
		RemoteName: "origin",
		RefSpecs: []config.RefSpec{
			config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", m.cfg.Branch, m.cfg.Branch)),
		},
	}); err != nil {
		return "", fmt.Errorf("git push (remove): %w", err)
	}
	return hash.String(), nil
}

// ReadFileAtCommit returns the content of a file at a specific commit SHA.
func (m *Manager) ReadFileAtCommit(commitSHA, relativePath string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	repo, err := gogit.PlainOpen(m.path)
	if err != nil {
		return "", fmt.Errorf("opening repo: %w", err)
	}

	commit, err := repo.CommitObject(plumbing.NewHash(commitSHA))
	if err != nil {
		return "", fmt.Errorf("loading commit %s: %w", commitSHA, err)
	}

	file, err := commit.File(relativePath)
	if err != nil {
		return "", err
	}

	r, err := file.Reader()
	if err != nil {
		return "", err
	}
	defer r.Close()

	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (m *Manager) auth() *http.BasicAuth {
	return &http.BasicAuth{Username: m.cfg.Username, Password: m.cfg.Token}
}

func isNonFastForward(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "non-fast-forward") || strings.Contains(s, "rejected")
}

func changedFiles(c *object.Commit) ([]string, error) {
	if c.NumParents() == 0 {
		// Initial commit — list all files in the tree.
		var files []string
		tree, err := c.Tree()
		if err != nil {
			return nil, err
		}
		tree.Files().ForEach(func(f *object.File) error {
			files = append(files, f.Name)
			return nil
		})
		return files, nil
	}

	parent, err := c.Parents().Next()
	if err != nil {
		return nil, err
	}
	patch, err := parent.Patch(c)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	for _, fp := range patch.FilePatches() {
		from, to := fp.Files()
		if to != nil && !seen[to.Path()] {
			seen[to.Path()] = true
		} else if from != nil && !seen[from.Path()] {
			seen[from.Path()] = true
		}
	}

	files := make([]string, 0, len(seen))
	for p := range seen {
		files = append(files, p)
	}
	return files, nil
}

func repoSlug(repoURL string) string {
	// Turn https://github.com/ORG/REPO.git → org-repo
	s := strings.TrimSuffix(repoURL, ".git")
	parts := strings.Split(s, "/")
	if len(parts) >= 2 {
		return strings.ToLower(parts[len(parts)-2] + "-" + parts[len(parts)-1])
	}
	return strings.ToLower(strings.NewReplacer(":", "-", "/", "-", ".", "-").Replace(s))
}

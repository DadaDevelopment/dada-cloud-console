package git

import (
	"errors"
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

// ErrNoPreviousVersion is returned by PreviousFileContent when a file has only a
// single version in history — there is nothing to roll back to.
var ErrNoPreviousVersion = errors.New("no previous version to roll back to")

// ErrHistoryRewritten is returned by CommitsSince when the stored cursor SHA is
// no longer an ancestor of the current HEAD — the remote history was rewritten
// (reset, force-push, or rebase) since the last sync. Walking history in this
// state would replay every commit back to the repo root instead of stopping at
// the cursor, re-running historical adds (incident 2026-07-23: a rewritten
// clone replayed history and resurrected 13 deleted projects). Callers must not
// attempt to process any commits when this error is returned; they should
// adopt the new HEAD as the cursor instead.
var ErrHistoryRewritten = errors.New("remote history rewritten since last sync cursor")

// LocalCloneError marks a failure that came from the local clone — its object
// store, index, or worktree — rather than from the network or the remote. The
// distinction is what lets SyncHard decide it is safe to throw the clone away:
// a fetch that fails because GitHub is unreachable must be retried, but a reset
// that fails because a pack is truncated will fail the same way forever.
type LocalCloneError struct{ Err error }

func (e *LocalCloneError) Error() string { return e.Err.Error() }

func (e *LocalCloneError) Unwrap() error { return e.Err }

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
	// Deleted holds the subset of Files that were removed in this commit
	// (present in the parent tree, absent at this commit). Consumers use it to
	// skip removed paths — e.g. so a DeleteProject that git-rm's a project tree
	// does not auto-recreate the project it just deleted.
	Deleted map[string]bool
}

// Manager owns a local clone of one remote repository.
// It serialises all git operations with a mutex so the DB Watcher
// and Git Watcher can share the same manager safely.
type Manager struct {
	cfg  RepoConfig
	path string // absolute path to the local clone
	mu   sync.Mutex
	// prePush runs immediately before every push with the hash just
	// committed. It is nil in production and exists so tests can advance the
	// remote branch inside the window between fetch and push — the only way
	// to reproduce a lost push race deterministically.
	prePush func(plumbing.Hash)
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
	return m.clone()
}

// clone performs the initial clone. Callers must hold m.mu.
func (m *Manager) clone() error {
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
	if err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return "", fmt.Errorf("pulling: %w", err)
	}

	head, err := repo.Head()
	if err != nil {
		return "", err
	}
	return head.Hash().String(), nil
}

// SyncHard makes the worktree bit-for-bit identical to the remote branch:
// fetch, hard reset (which also removes paths the remote deleted and drops any
// stale staged edit), then sweep untracked leftovers.
//
// Read-only consumers that answer "does this path exist in git?" by stat'ing
// the worktree MUST use this instead of Pull. Pull is a go-git merge: on a
// long-lived PVC clone whose index or worktree has drifted it leaves the
// checkout alone, so files deleted upstream survive on disk forever and every
// existence probe keeps answering yes. That is how the orphan GC came to see
// app.yaml for apps deleted months earlier and never pruned their snapshots,
// leaving the console listing apps that do not exist (2026-07-31).
// A local clone that cannot be reset is not recoverable in place, and the damage
// is permanent: go-git writes one pack per fetch and never repacks, so a
// long-lived clone accumulates hundreds of packs and a single truncated one
// makes every later reset fail identically with "unexpected EOF". Every consumer
// of SyncHard is a read-only probe with no local commits, so the clone is a
// disposable cache — discard it and clone again rather than leaving the caller
// permanently unable to verify anything. Observed 2026-08-04: the orphan GC had
// been failing on every tick for every project, so no stale snapshot anywhere in
// the estate could be purged.
func (m *Manager) SyncHard() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	err := m.syncHard()
	var local *LocalCloneError
	if !errors.As(err, &local) {
		return err
	}

	log.Warn().Err(err).Str("path", m.path).
		Msg("local clone unusable; discarding it and cloning again")
	if rmErr := os.RemoveAll(m.path); rmErr != nil {
		return fmt.Errorf("%w (discarding clone failed: %w)", err, rmErr)
	}
	if clErr := m.clone(); clErr != nil {
		return fmt.Errorf("%w (re-clone failed: %w)", err, clErr)
	}
	return m.syncHard()
}

// syncHard is SyncHard without the re-clone recovery. Callers must hold m.mu.
func (m *Manager) syncHard() error {
	repo, err := gogit.PlainOpen(m.path)
	if err != nil {
		return &LocalCloneError{fmt.Errorf("opening repo: %w", err)}
	}
	wt, err := repo.Worktree()
	if err != nil {
		return &LocalCloneError{err}
	}
	if err := m.resetToRemoteHead(repo, wt); err != nil {
		return err
	}
	if err := wt.Clean(&gogit.CleanOptions{Dir: true}); err != nil {
		return &LocalCloneError{fmt.Errorf("cleaning untracked files: %w", err)}
	}
	return nil
}

// FileChange is one file write in a git commit.
type FileChange struct {
	Path    string
	Content string
}

// pushAttempts bounds how many times a commit is rebuilt on top of a moving
// remote branch before the push is reported as failed. Only a proven race
// (the remote branch advanced under us) consumes an attempt.
const pushAttempts = 3

// CommitAndPush writes content to relativePath, commits, and pushes.
// A push lost to a concurrent writer is rebuilt on the new remote head and
// retried; see pushWithRaceRetry. Returns the commit SHA.
func (m *Manager) CommitAndPush(relativePath, content, commitMessage, authorName, authorEmail string) (string, error) {
	return m.CommitFilesAndPush([]FileChange{{Path: relativePath, Content: content}}, commitMessage, authorName, authorEmail)
}

// CommitFilesAndPush writes one or more files, commits, and pushes.
// Returns the commit SHA.
func (m *Manager) CommitFilesAndPush(files []FileChange, commitMessage, authorName, authorEmail string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.pushWithRaceRetry(func() (string, string, error) {
		return m.writeFilesCommitPush(files, commitMessage, authorName, authorEmail)
	})
}

// pushWithRaceRetry runs attempt until it succeeds, is proven already
// delivered, or fails for a reason that is not a lost race.
//
// Whether a failed push is retryable is decided by state, not by the wording
// of the error: after a failure the remote branch is re-read, and the push is
// retried only when that branch has moved away from the base the attempt
// committed on — the signature of a concurrent writer. Matching the error
// text cannot do this, because every git server phrases the same race
// differently ("non-fast-forward", "fetch first", "cannot lock ref '...': is
// at X but expected Y"), and an unmatched phrasing turned a race that only
// needed a retry into a terminal deploy failure for a user (2026-08-06).
//
// attempt returns the commit SHA and the remote head it built on; it must
// return both on failure too, and must start from the current remote head so
// a retry rebuilds on top of the winner instead of clobbering it. Callers
// must hold m.mu.
func (m *Manager) pushWithRaceRetry(attempt func() (string, string, error)) (string, error) {
	var lastErr error
	for try := 1; try <= pushAttempts; try++ {
		sha, base, err := attempt()
		if err == nil {
			return sha, nil
		}
		lastErr = err

		var localErr *LocalCloneError
		if base == "" || errors.As(err, &localErr) {
			break
		}

		head, headErr := m.remoteBranchHead()
		if headErr != nil {
			lastErr = fmt.Errorf("%w (re-reading remote branch failed: %w)", err, headErr)
			break
		}
		if delivered, derr := commitReachable(m.path, sha, head); derr == nil && delivered {
			log.Warn().Err(err).Str("sha", sha).
				Msg("push reported an error but the commit is on the remote branch; treating it as delivered")
			return sha, nil
		}
		if head.String() == base {
			break
		}
		if try < pushAttempts {
			log.Warn().Err(err).Int("attempt", try).
				Str("base", base).Str("remote_head", head.String()).
				Msg("push lost a race with a concurrent writer; rebuilding on the new remote head and retrying")
		}
	}

	return "", lastErr
}

// commitReachable reports whether sha is head or one of its ancestors, i.e.
// whether the commit is already on the remote branch despite the push having
// reported an error.
func commitReachable(path, sha string, head plumbing.Hash) (bool, error) {
	if sha == "" {
		return false, nil
	}
	hash := plumbing.NewHash(sha)
	if hash == head {
		return true, nil
	}

	repo, err := gogit.PlainOpen(path)
	if err != nil {
		return false, err
	}
	commit, err := repo.CommitObject(hash)
	if err != nil {
		return false, err
	}
	headCommit, err := repo.CommitObject(head)
	if err != nil {
		return false, err
	}
	return commit.IsAncestor(headCommit)
}

// writeFilesCommitPush returns the pushed commit SHA and the remote head the
// commit was built on. Both are returned on failure too, so the caller can
// tell a lost race from a terminal error.
func (m *Manager) writeFilesCommitPush(files []FileChange, commitMessage, authorName, authorEmail string) (string, string, error) {
	repo, err := gogit.PlainOpen(m.path)
	if err != nil {
		return "", "", fmt.Errorf("opening repo: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", "", err
	}

	if err := m.resetToRemoteHead(repo, wt); err != nil {
		return "", "", err
	}

	base, err := repo.Head()
	if err != nil {
		return "", "", fmt.Errorf("resolving commit base: %w", err)
	}
	baseSHA := base.Hash().String()

	for _, file := range files {
		absPath := filepath.Join(m.path, file.Path)
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return "", baseSHA, fmt.Errorf("mkdir %s: %w", file.Path, err)
		}
		if err := os.WriteFile(absPath, []byte(file.Content), 0o644); err != nil {
			return "", baseSHA, fmt.Errorf("writing file %s: %w", file.Path, err)
		}
		if _, err := wt.Add(file.Path); err != nil {
			return "", baseSHA, fmt.Errorf("git add %s: %w", file.Path, err)
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
		return "", baseSHA, fmt.Errorf("git commit: %w", err)
	}

	if m.prePush != nil {
		m.prePush(hash)
	}

	if err := repo.Push(&gogit.PushOptions{
		Auth:       m.auth(),
		RemoteName: "origin",
		RefSpecs: []config.RefSpec{
			config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", m.cfg.Branch, m.cfg.Branch)),
		},
	}); err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return hash.String(), baseSHA, fmt.Errorf("git push: %w", err)
	}

	return hash.String(), baseSHA, nil
}

// resetToRemoteHead fetches the tracked branch and hard-resets the worktree,
// index, and local branch to the freshly fetched remote HEAD. go-git's
// wt.Commit commits the ENTIRE index, so any stale staged edit left in the
// long-lived PVC clone would silently ride along with an unrelated commit and
// roll back files nobody touched (incident 2026-07-23: bootstrap commit
// reverted prod image pins). Every commit therefore starts from a worktree
// that is bit-for-bit identical to the remote branch.
func (m *Manager) resetToRemoteHead(repo *gogit.Repository, wt *gogit.Worktree) error {
	if err := repo.Fetch(&gogit.FetchOptions{
		Auth:       m.auth(),
		RemoteName: "origin",
		RefSpecs: []config.RefSpec{
			config.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", m.cfg.Branch, m.cfg.Branch)),
		},
		Force: true,
	}); err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return fmt.Errorf("fetch before commit: %w", err)
	}

	ref, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", m.cfg.Branch), true)
	if err != nil {
		return fmt.Errorf("resolving remote HEAD before commit: %w", err)
	}

	if err := wt.Reset(&gogit.ResetOptions{Mode: gogit.HardReset, Commit: ref.Hash()}); err != nil {
		return &LocalCloneError{fmt.Errorf("hard reset to remote HEAD %s: %w", ref.Hash(), err)}
	}
	return nil
}

// remoteBranchHead fetches the tracked branch and returns its hash without
// touching the worktree. Callers must hold m.mu.
func (m *Manager) remoteBranchHead() (plumbing.Hash, error) {
	repo, err := gogit.PlainOpen(m.path)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	if err := repo.Fetch(&gogit.FetchOptions{
		Auth:       m.auth(),
		RemoteName: "origin",
		RefSpecs: []config.RefSpec{
			config.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", m.cfg.Branch, m.cfg.Branch)),
		},
		Force: true,
	}); err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return plumbing.ZeroHash, fmt.Errorf("fetch: %w", err)
	}

	ref, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", m.cfg.Branch), true)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("resolving remote HEAD: %w", err)
	}
	return ref.Hash(), nil
}

// RemoteHEAD returns the current remote HEAD SHA without modifying the local clone.
func (m *Manager) RemoteHEAD() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	hash, err := m.remoteBranchHead()
	if err != nil {
		return "", err
	}
	return hash.String(), nil
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

	if fromSHA != "" {
		rewritten, err := historyRewritten(repo, fromSHA, head.Hash())
		if err != nil {
			return nil, err
		}
		if rewritten {
			return nil, ErrHistoryRewritten
		}
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

		files, deleted, ferr := changedFiles(c)
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
			Deleted: deleted,
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

// LocalHEAD returns the current local clone's HEAD SHA without fetching.
// Callers that just ran CommitsSince (which pulls first) can use this to learn
// the new tip after an ErrHistoryRewritten result.
func (m *Manager) LocalHEAD() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	repo, err := gogit.PlainOpen(m.path)
	if err != nil {
		return "", fmt.Errorf("opening repo: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", err
	}
	return head.Hash().String(), nil
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

	return m.pushWithRaceRetry(func() (string, string, error) {
		return m.removeFilesCommitPush(relativePaths, commitMessage, authorName, authorEmail)
	})
}

// removeFilesCommitPush returns the pushed commit SHA and the remote head the
// commit was built on, both on failure too, so pushWithRaceRetry can tell a
// lost race from a terminal error. A deletion that finds nothing to delete
// returns an empty SHA and no error.
func (m *Manager) removeFilesCommitPush(relativePaths []string, commitMessage, authorName, authorEmail string) (string, string, error) {
	repo, err := gogit.PlainOpen(m.path)
	if err != nil {
		return "", "", fmt.Errorf("opening repo: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", "", err
	}
	if err := m.resetToRemoteHead(repo, wt); err != nil {
		return "", "", err
	}
	base, err := repo.Head()
	if err != nil {
		return "", "", fmt.Errorf("resolving commit base: %w", err)
	}
	baseSHA := base.Hash().String()

	removed := 0
	for _, rel := range relativePaths {
		abs := filepath.Join(m.path, rel)
		if _, err := os.Stat(abs); os.IsNotExist(err) {
			continue
		}
		if _, err := wt.Remove(rel); err != nil {
			return "", baseSHA, fmt.Errorf("git rm %s: %w", rel, err)
		}
		removed++
	}
	if removed == 0 {
		return "", baseSHA, nil
	}
	hash, err := wt.Commit(commitMessage, &gogit.CommitOptions{
		Author: &object.Signature{Name: authorName, Email: authorEmail, When: time.Now()},
	})
	if err != nil {
		return "", baseSHA, fmt.Errorf("git commit (remove): %w", err)
	}
	if m.prePush != nil {
		m.prePush(hash)
	}
	if err := repo.Push(&gogit.PushOptions{
		Auth:       m.auth(),
		RemoteName: "origin",
		RefSpecs: []config.RefSpec{
			config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", m.cfg.Branch, m.cfg.Branch)),
		},
	}); err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return hash.String(), baseSHA, fmt.Errorf("git push (remove): %w", err)
	}
	return hash.String(), baseSHA, nil
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

// PreviousFileContent returns the content of relativePath as it was BEFORE the
// most recent commit that changed it — i.e. the rollback target. It scans the
// path-scoped history (newest first): the 1st entry is the current version, the
// 2nd is the previous one. Returns ErrNoPreviousVersion when the file has only
// ever had a single version (nothing to roll back to).
func (m *Manager) PreviousFileContent(relativePath string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.pull(); err != nil {
		return "", err
	}
	repo, err := gogit.PlainOpen(m.path)
	if err != nil {
		return "", fmt.Errorf("opening repo: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", err
	}
	path := relativePath
	iter, err := repo.Log(&gogit.LogOptions{From: head.Hash(), FileName: &path})
	if err != nil {
		return "", fmt.Errorf("log for %s: %w", relativePath, err)
	}
	defer iter.Close()

	// 1st = current version's commit; 2nd = the previous version.
	if _, err := iter.Next(); err != nil {
		return "", fmt.Errorf("no history for %s: %w", relativePath, err)
	}
	prev, err := iter.Next()
	if err != nil {
		return "", ErrNoPreviousVersion
	}
	file, err := prev.File(relativePath)
	if err != nil {
		return "", fmt.Errorf("file at previous commit: %w", err)
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
	if m.cfg.Username == "" && m.cfg.Token == "" {
		return nil
	}
	return &http.BasicAuth{Username: m.cfg.Username, Password: m.cfg.Token}
}

// historyRewritten reports whether fromSHA is no longer reachable as an
// ancestor of headHash — i.e. the branch was reset, force-pushed, or rebased
// since fromSHA was recorded as the sync cursor. A commit that no longer
// exists in the repo (pruned by gc after an orphaning rewrite) also counts as
// rewritten.
func historyRewritten(repo *gogit.Repository, fromSHA string, headHash plumbing.Hash) (bool, error) {
	fromCommit, err := repo.CommitObject(plumbing.NewHash(fromSHA))
	if err != nil {
		return true, nil
	}

	headCommit, err := repo.CommitObject(headHash)
	if err != nil {
		return false, fmt.Errorf("loading HEAD commit: %w", err)
	}

	isAncestor, err := fromCommit.IsAncestor(headCommit)
	if err != nil {
		return false, fmt.Errorf("checking ancestry of cursor %s: %w", fromSHA, err)
	}
	return !isAncestor, nil
}

func changedFiles(c *object.Commit) ([]string, map[string]bool, error) {
	deleted := map[string]bool{}
	if c.NumParents() == 0 {
		// Initial commit — list all files in the tree.
		var files []string
		tree, err := c.Tree()
		if err != nil {
			return nil, nil, err
		}
		tree.Files().ForEach(func(f *object.File) error {
			files = append(files, f.Name)
			return nil
		})
		return files, deleted, nil
	}

	parent, err := c.Parents().Next()
	if err != nil {
		return nil, nil, err
	}
	patch, err := parent.Patch(c)
	if err != nil {
		return nil, nil, err
	}

	seen := map[string]bool{}
	for _, fp := range patch.FilePatches() {
		from, to := fp.Files()
		if to != nil && !seen[to.Path()] {
			seen[to.Path()] = true
		} else if from != nil && !seen[from.Path()] {
			seen[from.Path()] = true
		}
		if to == nil && from != nil {
			deleted[from.Path()] = true
		}
	}

	files := make([]string, 0, len(seen))
	for p := range seen {
		files = append(files, p)
	}
	return files, deleted, nil
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

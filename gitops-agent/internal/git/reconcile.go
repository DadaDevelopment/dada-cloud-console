package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/rs/zerolog/log"
)

// maxLocalCommits bounds how many unpushed local commits a clone may carry
// before reconciliation stops trying to replay them one by one and drops them
// all. The agent pushes every commit it makes, so more than a handful means the
// clone is not in a state any single interruption explains.
const maxLocalCommits = 20

// errReplayConflict reports that a stranded local commit cannot be replayed
// because the remote changed one of its files since the commit's parent.
var errReplayConflict = errors.New("stranded local commit conflicts with the remote branch")

// ReconcileResult describes what reconciliation did with commits that existed
// only in the local clone. Replayed maps each stranded commit SHA to the SHA it
// now has on the remote branch (identical when the remote had not moved).
// Dropped lists commits that could not be replayed and were discarded; the
// operations that produced them must run again, because their change never
// reached the remote.
type ReconcileResult struct {
	RepoURL  string
	Branch   string
	Replayed map[string]string
	Dropped  []Commit
}

var reconcileHook func(ReconcileResult)

// SetReconcileHook registers the function told about every reconciliation, so
// the operations table can follow a replayed commit to its new SHA and put the
// operations of dropped commits back in the queue. Set it once at startup.
func SetReconcileHook(f func(ReconcileResult)) {
	reconcileHook = f
}

// reconcileLocalCommits makes the local branch agree with the remote before a
// pull, so a clone that holds commits the remote never received cannot wedge.
//
// A commit becomes stranded when the agent is killed between committing and
// pushing (a rollout, an OOM kill, an eviction). The clone lives on a PVC, so
// the next pod inherits it. Two failures followed on 2026-09-24. While the
// remote had not moved, pull reported "already up to date" and the stranded
// file read as delivered, so a resize recorded Committed against a SHA that
// existed only on the PVC. Once anyone else pushed, the fast-forward-only pull
// failed with "non-fast-forward update" on every tick and every operation, and
// no deploy on the platform went through for 1h37m.
//
// Stranded commits are replayed on top of the fetched remote with their
// original author, committer and message and pushed, so the change they carry
// reaches the remote and, when the remote had not moved, keeps its SHA. When
// the remote changed one of their files in the meantime they are dropped and
// the hook is told, so their operations run again against the current state
// instead of silently losing the change. Callers must hold m.mu.
func (m *Manager) reconcileLocalCommits() error {
	remote, err := m.remoteBranchHead()
	if err != nil {
		return err
	}
	repo, err := gogit.PlainOpen(m.path)
	if err != nil {
		return &LocalCloneError{fmt.Errorf("opening repo: %w", err)}
	}
	head, err := repo.Head()
	if err != nil {
		return &LocalCloneError{fmt.Errorf("resolving local HEAD: %w", err)}
	}
	if head.Hash() == remote {
		return nil
	}

	local, err := localOnlyCommits(repo, head.Hash(), remote)
	if err != nil && !errors.Is(err, errReplayConflict) {
		return err
	}
	if len(local) == 0 {
		return nil
	}

	result := ReconcileResult{RepoURL: m.cfg.RepoURL, Branch: m.cfg.Branch}
	shas := make([]string, 0, len(local))
	for _, c := range local {
		shas = append(shas, c.Hash.String())
	}
	log.Warn().Strs("stranded", shas).Str("remote_head", remote.String()).
		Msg("local clone holds commits the remote never received; replaying them onto the remote branch")

	var replayed map[string]string
	if err == nil {
		_, err = m.pushWithRaceRetry(func() (string, string, error) {
			replayed = map[string]string{}
			return m.replayOntoRemote(local, replayed)
		})
	}
	if err == nil {
		result.Replayed = replayed
		log.Warn().Interface("replayed", replayed).Msg("stranded local commits replayed and pushed")
		notifyReconcile(result)
		return nil
	}
	if !errors.Is(err, errReplayConflict) && len(local) <= maxLocalCommits {
		return err
	}

	log.Error().Err(err).Strs("dropped", shas).
		Msg("stranded local commits cannot be replayed; resetting the clone to the remote and re-queuing their operations")
	if err := m.resetClone(); err != nil {
		return err
	}
	for _, c := range local {
		result.Dropped = append(result.Dropped, Commit{
			SHA:     c.Hash.String(),
			Message: c.Message,
			Author:  c.Author.Name,
			Email:   c.Author.Email,
			When:    c.Author.When,
		})
	}
	notifyReconcile(result)
	return nil
}

func notifyReconcile(r ReconcileResult) {
	if reconcileHook != nil {
		reconcileHook(r)
	}
}

// resetClone hard-resets the clone to the freshly fetched remote branch.
func (m *Manager) resetClone() error {
	repo, err := gogit.PlainOpen(m.path)
	if err != nil {
		return &LocalCloneError{fmt.Errorf("opening repo: %w", err)}
	}
	wt, err := repo.Worktree()
	if err != nil {
		return &LocalCloneError{err}
	}
	return m.resetToRemoteHead(repo, wt)
}

// localOnlyCommits returns the commits reachable from head that the remote
// branch does not contain, oldest first. A local branch that is merely behind
// the remote has none. More than maxLocalCommits is reported as a conflict so
// the caller drops them rather than replaying an unbounded history.
func localOnlyCommits(repo *gogit.Repository, head, remote plumbing.Hash) ([]*object.Commit, error) {
	remoteCommit, err := repo.CommitObject(remote)
	if err != nil {
		return nil, fmt.Errorf("reading remote head %s: %w", remote, err)
	}
	c, err := repo.CommitObject(head)
	if err != nil {
		return nil, &LocalCloneError{fmt.Errorf("reading local head %s: %w", head, err)}
	}

	var out []*object.Commit
	for {
		if c.Hash == remote {
			break
		}
		onRemote, err := c.IsAncestor(remoteCommit)
		if err != nil {
			return nil, fmt.Errorf("checking %s against the remote: %w", c.Hash, err)
		}
		if onRemote {
			break
		}
		out = append(out, c)
		if len(out) > maxLocalCommits {
			return out, fmt.Errorf("%w: more than %d local commits", errReplayConflict, maxLocalCommits)
		}
		if c.NumParents() == 0 {
			break
		}
		if c, err = c.Parent(0); err != nil {
			return nil, fmt.Errorf("walking local history: %w", err)
		}
	}

	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// replayOntoRemote rebuilds each stranded commit on top of the remote head and
// pushes the result. It follows the pushWithRaceRetry contract: it returns the
// last replayed SHA and the remote head it built on, both also on failure, and
// starts from a fresh remote head so a retry after a lost race rebuilds on top
// of the winner. replayed receives old SHA -> new SHA for every commit made.
func (m *Manager) replayOntoRemote(local []*object.Commit, replayed map[string]string) (string, string, error) {
	repo, err := gogit.PlainOpen(m.path)
	if err != nil {
		return "", "", &LocalCloneError{fmt.Errorf("opening repo: %w", err)}
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", "", &LocalCloneError{err}
	}
	if err := m.resetToRemoteHead(repo, wt); err != nil {
		return "", "", err
	}
	base, err := repo.Head()
	if err != nil {
		return "", "", fmt.Errorf("resolving replay base: %w", err)
	}
	baseSHA := base.Hash().String()

	var last plumbing.Hash
	for _, c := range local {
		if err := m.applyCommit(wt, c); err != nil {
			return "", baseSHA, err
		}
		author, committer := c.Author, c.Committer
		hash, err := wt.Commit(c.Message, &gogit.CommitOptions{Author: &author, Committer: &committer})
		if err != nil {
			return "", baseSHA, fmt.Errorf("replaying %s: %w", c.Hash, err)
		}
		replayed[c.Hash.String()] = hash.String()
		last = hash
	}

	if err := repo.Push(&gogit.PushOptions{
		Auth:       m.auth(),
		RemoteName: "origin",
		RefSpecs: []config.RefSpec{
			config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", m.cfg.Branch, m.cfg.Branch)),
		},
	}); err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return last.String(), baseSHA, fmt.Errorf("pushing replayed commits: %w", err)
	}
	return last.String(), baseSHA, nil
}

// applyCommit writes the changes of c into the worktree and stages them,
// refusing with errReplayConflict when a file it touches no longer holds the
// content c was built on, when c is a merge, or when it renames a file.
func (m *Manager) applyCommit(wt *gogit.Worktree, c *object.Commit) error {
	if c.NumParents() != 1 {
		return fmt.Errorf("%w: %s has %d parents", errReplayConflict, c.Hash, c.NumParents())
	}
	parent, err := c.Parent(0)
	if err != nil {
		return fmt.Errorf("reading parent of %s: %w", c.Hash, err)
	}
	patch, err := parent.Patch(c)
	if err != nil {
		return fmt.Errorf("diffing %s: %w", c.Hash, err)
	}

	for _, fp := range patch.FilePatches() {
		from, to := fp.Files()
		if from != nil && to != nil && from.Path() != to.Path() {
			return fmt.Errorf("%w: %s renames %s", errReplayConflict, c.Hash, from.Path())
		}
		path := ""
		if to != nil {
			path = to.Path()
		} else if from != nil {
			path = from.Path()
		}
		if path == "" {
			continue
		}

		want, wantOK, err := fileAt(parent, path)
		if err != nil {
			return err
		}
		have, haveOK, err := m.worktreeFile(path)
		if err != nil {
			return err
		}
		if wantOK != haveOK || want != have {
			return fmt.Errorf("%w: %s changed on the remote since %s", errReplayConflict, path, c.Hash)
		}

		if to == nil {
			if _, err := wt.Remove(path); err != nil {
				return fmt.Errorf("replaying delete of %s: %w", path, err)
			}
			continue
		}
		content, _, err := fileAt(c, path)
		if err != nil {
			return err
		}
		abs := filepath.Join(m.path, path)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", path, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		if _, err := wt.Add(path); err != nil {
			return fmt.Errorf("staging %s: %w", path, err)
		}
	}
	return nil
}

func fileAt(c *object.Commit, path string) (string, bool, error) {
	f, err := c.File(path)
	if errors.Is(err, object.ErrFileNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading %s at %s: %w", path, c.Hash, err)
	}
	content, err := f.Contents()
	if err != nil {
		return "", false, fmt.Errorf("reading %s at %s: %w", path, c.Hash, err)
	}
	return content, true, nil
}

func (m *Manager) worktreeFile(path string) (string, bool, error) {
	b, err := os.ReadFile(filepath.Join(m.path, path))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading %s from the worktree: %w", path, err)
	}
	return string(b), true, nil
}

// OperationIDFromMessage returns the operation id the agent writes into every
// commit message as an "Operation: <id>" line, or "" when there is none.
func OperationIDFromMessage(message string) string {
	for _, line := range strings.Split(message, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Operation:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

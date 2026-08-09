/**
 * Unit tests for lib/build-commit.ts.
 *
 * Run with Node's built-in test runner and type stripping:
 *
 *   cd frontend && npm run test:unit
 *
 * The property that matters: a synthetic `manual-<timestamp>` commit_sha
 * (what the backend writes for a manually triggered build before the real
 * HEAD commit is resolved) must never come back out of resolveCommit as a
 * "sha" — it must fall back to the branch, or to "none" when there is no
 * branch either (uploaded-archive apps).
 */

import test from "node:test";
import assert from "node:assert/strict";

import { formatCommitLabel, isPlaceholderCommit, resolveCommit, type Translate } from "./build-commit.ts";

const t: Translate = (key, vars) => {
  if (key === "common.commit.branchLatest") return `latest commit on branch ${vars?.branch}`;
  if (key === "common.commit.archiveWithId") return `archive ${vars?.id}`;
  if (key === "common.commit.archive") return "uploaded archive";
  return key;
};

test("isPlaceholderCommit matches only the manual- prefix", () => {
  assert.equal(isPlaceholderCommit("manual-1738528000"), true);
  assert.equal(isPlaceholderCommit("a1b2c3d4e5f6"), false);
  assert.equal(isPlaceholderCommit("manually-typed"), false, "must be an exact prefix, not a loose substring match");
  assert.equal(isPlaceholderCommit(undefined), false);
  assert.equal(isPlaceholderCommit(null), false);
  assert.equal(isPlaceholderCommit(""), false);
});

test("resolveCommit returns the real sha when commit_sha is not a placeholder", () => {
  const resolved = resolveCommit({ commit_sha: "a1b2c3d4e5f6", commit_message: "fix thing", branch: "main" });
  assert.deepEqual(resolved, { kind: "sha", sha: "a1b2c3d4e5f6", message: "fix thing" });
});

test("resolveCommit falls back to a resolved head_sha when commit_sha is the placeholder", () => {
  const resolved = resolveCommit({ commit_sha: "manual-1738528000", head_sha: "deadbeef1234", branch: "main" });
  assert.deepEqual(resolved, { kind: "sha", sha: "deadbeef1234", message: null });
});

test("resolveCommit never surfaces the placeholder sha itself", () => {
  const resolved = resolveCommit({ commit_sha: "manual-1738528000", branch: "main" });
  assert.notEqual(resolved.kind, "sha");
  assert.deepEqual(resolved, { kind: "branch", branch: "main" });
});

test("resolveCommit falls back to none when there is no branch either", () => {
  const resolved = resolveCommit({ commit_sha: "manual-1738528000" });
  assert.deepEqual(resolved, { kind: "none" });
});

test("resolveCommit treats a real empty branch/no branch consistently", () => {
  assert.deepEqual(resolveCommit({}), { kind: "none" });
});

test("resolveCommit reports an archive build with an identified upload id", () => {
  const resolved = resolveCommit({
    source: "archive",
    commit_sha: "manual-1738528000",
    head_sha: "a1b2c3d4",
    commit_message: "site-v2.zip",
    branch: "upload",
  });
  assert.deepEqual(resolved, { kind: "archive", uploadId: "a1b2c3d4", filename: "site-v2.zip" });
});

test("resolveCommit reports an archive build with no identified upload id", () => {
  const resolved = resolveCommit({ source: "archive", commit_sha: "manual-1738528000", branch: "upload" });
  assert.deepEqual(resolved, { kind: "archive", uploadId: null, filename: null });
});

test("resolveCommit never lets a stale branch: 'upload' masquerade as a git branch when source is archive", () => {
  const resolved = resolveCommit({ source: "archive", commit_sha: "manual-1738528000", branch: "upload" });
  assert.notEqual(resolved.kind, "branch");
  assert.equal(resolved.kind, "archive");
});

test("resolveCommit leaves git builds byte-identical to before the archive field existed", () => {
  const withSha = resolveCommit({ source: "git", commit_sha: "a1b2c3d4e5f6", commit_message: "fix thing", branch: "main" });
  assert.deepEqual(withSha, { kind: "sha", sha: "a1b2c3d4e5f6", message: "fix thing" });

  const withPlaceholderBranch = resolveCommit({ source: "git", commit_sha: "manual-1738528000", branch: "main" });
  assert.deepEqual(withPlaceholderBranch, { kind: "branch", branch: "main" });

  const withNoSource = resolveCommit({ commit_sha: "a1b2c3d4e5f6", commit_message: "fix thing", branch: "main" });
  assert.deepEqual(withNoSource, { kind: "sha", sha: "a1b2c3d4e5f6", message: "fix thing" });
});

test("formatCommitLabel renders a short sha, honest branch phrasing, identified archive, or archive phrasing", () => {
  assert.equal(formatCommitLabel({ kind: "sha", sha: "a1b2c3d4e5f6", message: null }, t), "a1b2c3d");
  assert.equal(formatCommitLabel({ kind: "branch", branch: "main" }, t), "latest commit on branch main");
  assert.equal(formatCommitLabel({ kind: "archive", uploadId: "a1b2c3d4", filename: "site.zip" }, t), "archive a1b2c3d4");
  assert.equal(formatCommitLabel({ kind: "archive", uploadId: null, filename: null }, t), "uploaded archive");
  assert.equal(formatCommitLabel({ kind: "none" }, t), "uploaded archive");
});

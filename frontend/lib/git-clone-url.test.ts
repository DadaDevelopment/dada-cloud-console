/**
 * Unit tests for lib/git-clone-url.ts.
 *
 * Run with Node's built-in test runner and type stripping (no npm ci needed):
 *
 *   cd frontend && npm run test:unit
 */

import test from "node:test";
import assert from "node:assert/strict";

import { parseGitCloneUrl } from "./git-clone-url.ts";

test("parses a gitlab.com URL with .git suffix", () => {
  const result = parseGitCloneUrl("https://gitlab.com/owner/repo.git");
  assert.ok(result.ok);
  assert.deepEqual(result.value, {
    provider: "gitlab",
    repoFullName: "owner/repo",
    cloneUrl: "https://gitlab.com/owner/repo.git",
  });
});

test("parses a gitlab.com URL without .git suffix", () => {
  const result = parseGitCloneUrl("https://gitlab.com/owner/repo");
  assert.ok(result.ok);
  assert.equal(result.value.repoFullName, "owner/repo");
  assert.equal(result.value.provider, "gitlab");
});

test("parses a github.com URL as provider github", () => {
  const result = parseGitCloneUrl("https://github.com/owner/repo.git");
  assert.ok(result.ok);
  assert.equal(result.value.provider, "github");
  assert.equal(result.value.repoFullName, "owner/repo");
});

test("treats a self-hosted GitLab/Gitea host as provider gitlab", () => {
  const result = parseGitCloneUrl("https://git.mycompany.ru/team/backend.git");
  assert.ok(result.ok);
  assert.equal(result.value.provider, "gitlab");
  assert.equal(result.value.repoFullName, "team/backend");
});

test("keeps the full path for a nested GitLab subgroup", () => {
  const result = parseGitCloneUrl("https://gitlab.com/group/subgroup/repo.git");
  assert.ok(result.ok);
  assert.equal(result.value.repoFullName, "group/subgroup/repo");
  assert.equal(result.value.provider, "gitlab");
});

test("strips a trailing slash before deriving the repo name", () => {
  const result = parseGitCloneUrl("https://gitlab.com/owner/repo/");
  assert.ok(result.ok);
  assert.equal(result.value.repoFullName, "owner/repo");
});

test("rejects an scp-style ssh URL", () => {
  const result = parseGitCloneUrl("git@gitlab.com:owner/repo.git");
  assert.ok(!result.ok);
  assert.equal(result.error, "ssh-not-supported");
});

test("rejects an ssh scheme URL", () => {
  const result = parseGitCloneUrl("ssh://git@gitlab.com/owner/repo.git");
  assert.ok(!result.ok);
  assert.equal(result.error, "ssh-not-supported");
});

test("rejects a plain http URL", () => {
  const result = parseGitCloneUrl("http://gitlab.com/owner/repo.git");
  assert.ok(!result.ok);
  assert.equal(result.error, "http-not-supported");
});

test("rejects garbage input", () => {
  for (const bad of ["", "  ", "not a url", "just-text", "https://"]) {
    const result = parseGitCloneUrl(bad);
    assert.ok(!result.ok, `${JSON.stringify(bad)} should be rejected`);
  }
});

test("rejects a URL missing a repo path segment", () => {
  const result = parseGitCloneUrl("https://gitlab.com/owner");
  assert.ok(!result.ok);
  assert.equal(result.error, "incomplete-path");
});

test("rejects a bare host with no path", () => {
  const result = parseGitCloneUrl("https://gitlab.com");
  assert.ok(!result.ok);
  assert.equal(result.error, "incomplete-path");
});

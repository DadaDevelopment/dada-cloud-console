import assert from "node:assert/strict";
import test from "node:test";
import { isStuckOnRepeat, repeatHintKey } from "./build-repeat.ts";

test("a first-time failure is not a repeat", () => {
  assert.equal(isStuckOnRepeat(1), false);
});

test("the second identical failure counts as stuck", () => {
  assert.equal(isStuckOnRepeat(2), true);
});

test("a third identical failure still counts as stuck", () => {
  assert.equal(isStuckOnRepeat(3), true);
});

test("a missing repeat_count is not treated as a repeat", () => {
  assert.equal(isStuckOnRepeat(undefined), false);
  assert.equal(isStuckOnRepeat(null), false);
});

test("picks the dependency-manifest hint for dockerfile_build_failed", () => {
  assert.equal(repeatHintKey("dockerfile_build_failed"), "apps.latestBuild.failed.repeatHint.dockerfileBuildFailed");
});

test("picks the reconnect hint for git_auth_failed", () => {
  assert.equal(repeatHintKey("git_auth_failed"), "apps.latestBuild.failed.repeatHint.gitAuthFailed");
});

test("picks the platform-owned hint for platform_error", () => {
  assert.equal(repeatHintKey("platform_error"), "apps.latestBuild.failed.repeatHint.platformError");
});

test("falls back to the generic hint for an unknown or missing reason", () => {
  assert.equal(repeatHintKey("no_dockerfile"), "apps.latestBuild.failed.repeatHint.generic");
  assert.equal(repeatHintKey(undefined), "apps.latestBuild.failed.repeatHint.generic");
  assert.equal(repeatHintKey(null), "apps.latestBuild.failed.repeatHint.generic");
});

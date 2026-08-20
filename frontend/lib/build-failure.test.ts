import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import { buildFailureDetail, buildFailureSummary, isRepoFixable, needsRepoReconnect } from "./build-failure.ts";

test("drops the fail_reason prefix the build agent writes", () => {
  assert.equal(
    buildFailureDetail(
      "dockerfile_build_failed",
      "dockerfile_build_failed: [stage-1 4/6] RUN pip install -r requirements.txt: ERROR: No matching distribution found for sqlite3",
    ),
    "[stage-1 4/6] RUN pip install -r requirements.txt: ERROR: No matching distribution found for sqlite3",
  );
});

test("leaves a message that carries no prefix alone", () => {
  assert.equal(
    buildFailureDetail("platform_error", "build orphaned: build-agent restarted before completion; retry"),
    "build orphaned: build-agent restarted before completion; retry",
  );
});

test("does not strip a different code that happens to lead the message", () => {
  assert.equal(
    buildFailureDetail("build_failed", "dockerfile_build_failed: something else"),
    "dockerfile_build_failed: something else",
  );
});

test("survives a missing reason or message", () => {
  assert.equal(buildFailureDetail(undefined, "plain failure"), "plain failure");
  assert.equal(buildFailureDetail("build_failed", null), "");
  assert.equal(buildFailureDetail(null, undefined), "");
});

test("the autofix summary names the reason and the cause", () => {
  assert.equal(
    buildFailureSummary({
      branch: "main",
      commitRef: "abcdef123456",
      commitMessage: "add sqlite",
      failReason: "dockerfile_build_failed",
      errorMessage:
        "dockerfile_build_failed: [4/7] RUN pip install -r requirements.txt: ERROR: No matching distribution found for sqlite3",
    }),
    "Build failed on branch main (abcdef123456): add sqlite\n" +
      "Failure reason: dockerfile_build_failed\n" +
      "Cause: [4/7] RUN pip install -r requirements.txt: ERROR: No matching distribution found for sqlite3",
  );
});

test("a build with nothing persisted still produces the old summary", () => {
  assert.equal(
    buildFailureSummary({ branch: "main", commitRef: "abc123" }),
    "Build failed on branch main (abc123)",
  );
});

test("failures the platform caused are not offered to the repo-editing agent", () => {
  assert.equal(isRepoFixable("git_auth_failed"), false);
  assert.equal(isRepoFixable("platform_error"), false);
  assert.equal(isRepoFixable("dockerfile_build_failed"), true);
  assert.equal(isRepoFixable("no_dockerfile"), true);
  assert.equal(isRepoFixable(null), true);
});

test("only a broken git link asks the user to reconnect the repository", () => {
  assert.equal(needsRepoReconnect("git_auth_failed"), true);
  assert.equal(needsRepoReconnect("platform_error"), false);
  assert.equal(needsRepoReconnect("app_deleted"), false);
  assert.equal(needsRepoReconnect("dockerfile_build_failed"), false);
  assert.equal(needsRepoReconnect(null), false);
  assert.equal(needsRepoReconnect(undefined), false);
});

test("the failed-build card gates the reconnect CTA on the git link, not on the autofix predicate", () => {
  const card = fs.readFileSync(
    path.join(import.meta.dirname, "..", "components", "deploy", "app-latest-build-card.tsx"),
    "utf8",
  );
  assert.match(card, /app_latest_build:reconnect_repo/);
  assert.match(card, /needsRepoReconnect\(build\.fail_reason\) && \(/);
  assert.equal(
    card.includes("!isRepoFixable("),
    false,
    "negating isRepoFixable sends platform_error and app_deleted to a reconnect that cannot help",
  );
});

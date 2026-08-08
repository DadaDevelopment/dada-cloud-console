import assert from "node:assert/strict";
import test from "node:test";
import { buildFailureDetail } from "./build-failure.ts";

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

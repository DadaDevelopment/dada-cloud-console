/**
 * Unit tests for classifyConnectRepoConflict in lib/api.ts.
 *
 * Run with Node's built-in test runner and type stripping (no npm ci needed):
 *
 *   cd frontend && npm run test:unit
 */

import test from "node:test";
import assert from "node:assert/strict";

import { classifyConnectRepoConflict } from "./api.ts";

test("409 with code repo_already_connected classifies as repo_already_connected", () => {
  assert.equal(classifyConnectRepoConflict(409, "repo_already_connected"), "repo_already_connected");
});

test("409 with code app_name_taken classifies as app_name_taken", () => {
  assert.equal(classifyConnectRepoConflict(409, "app_name_taken"), "app_name_taken");
});

test("409 with no code falls back to app_name_taken (old backend during rollout)", () => {
  assert.equal(classifyConnectRepoConflict(409, undefined), "app_name_taken");
});

test("409 with an unrecognized code falls back to app_name_taken", () => {
  assert.equal(classifyConnectRepoConflict(409, "something_else"), "app_name_taken");
});

test("non-409 status is not a conflict", () => {
  assert.equal(classifyConnectRepoConflict(500, "repo_already_connected"), null);
  assert.equal(classifyConnectRepoConflict(undefined, "repo_already_connected"), null);
});

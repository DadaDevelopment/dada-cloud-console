/**
 * Unit tests for isSignupClosedError in lib/api.ts - the status+code gate
 * that lets the console tell "registration is closed" apart from a stale
 * session (401) or an unrelated 403, without ever branching on error prose.
 *
 * Run with Node's built-in test runner and type stripping (no npm ci needed):
 *
 *   cd frontend && npm run test:unit
 */

import test from "node:test";
import assert from "node:assert/strict";

import { isSignupClosedError } from "./api.ts";

test("403 with code signup_closed is the signup gate", () => {
  assert.equal(isSignupClosedError(403, "signup_closed"), true);
});

test("403 with no code is not the signup gate (old behavior: generic forbidden)", () => {
  assert.equal(isSignupClosedError(403, undefined), false);
});

test("403 with a different code is not the signup gate", () => {
  assert.equal(isSignupClosedError(403, "some_other_reason"), false);
});

test("401 is not the signup gate, regardless of code (old behavior: stale session)", () => {
  assert.equal(isSignupClosedError(401, "signup_closed"), false);
  assert.equal(isSignupClosedError(401, undefined), false);
});

test("signup_closed code on a non-403 status is not the signup gate", () => {
  assert.equal(isSignupClosedError(500, "signup_closed"), false);
  assert.equal(isSignupClosedError(undefined, "signup_closed"), false);
});

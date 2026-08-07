/**
 * Unit tests for the /callback verdict (lib/callback-outcome.ts).
 *
 * Run with Node's built-in test runner and type stripping:
 *
 *   cd frontend && npm run test:unit
 *
 * These pin the second half of the "eternal spinner" story. auth-retry
 * covers failures the auth provider can see; this covers the one it cannot:
 * a failed authorize round-trip is reported by the SSO library as a plain
 * `unauthenticated`, indistinguishable from a logged-out visitor, and the
 * callback route must still turn that into a visible dead end.
 */

import test from "node:test";
import assert from "node:assert/strict";

import { callbackVerdict, parseCallbackError } from "./callback-outcome.ts";

test("parseCallbackError: reads the OAuth error code off the redirect_uri", () => {
  assert.equal(parseCallbackError("?error=access_denied&state=abc"), "access_denied");
  assert.equal(parseCallbackError("error=invalid_grant"), "invalid_grant");
});

test("parseCallbackError: a clean callback carries no error", () => {
  assert.equal(parseCallbackError("?code=xyz&state=abc"), null);
  assert.equal(parseCallbackError(""), null);
  assert.equal(parseCallbackError("?error="), null);
});

test("callbackVerdict: still loading keeps the spinner", () => {
  assert.deepEqual(callbackVerdict({ isLoading: true, hasToken: false, hasAuthError: false, search: "?code=xyz" }), {
    state: "pending",
  });
});

test("callbackVerdict: a token means the sign-in worked", () => {
  assert.deepEqual(callbackVerdict({ isLoading: false, hasToken: true, hasAuthError: false, search: "?code=xyz" }), {
    state: "authenticated",
  });
});

test("callbackVerdict: settled with no token is a dead end, not a spinner", () => {
  assert.deepEqual(callbackVerdict({ isLoading: false, hasToken: false, hasAuthError: false, search: "" }), {
    state: "failed",
    reason: "callback",
    error: null,
  });
});

test("callbackVerdict: a denied consent is called out separately from a breakage", () => {
  assert.deepEqual(
    callbackVerdict({ isLoading: false, hasToken: false, hasAuthError: false, search: "?error=access_denied" }),
    { state: "failed", reason: "denied", error: "access_denied" },
  );
});

test("callbackVerdict: a reused authorization code reads as a breakage worth retrying", () => {
  assert.deepEqual(
    callbackVerdict({ isLoading: false, hasToken: false, hasAuthError: false, search: "?error=invalid_grant" }),
    { state: "failed", reason: "callback", error: "invalid_grant" },
  );
});

test("callbackVerdict: a provider-level authError wins over the loading flag", () => {
  assert.deepEqual(callbackVerdict({ isLoading: true, hasToken: false, hasAuthError: true, search: "" }), {
    state: "failed",
    reason: "callback",
    error: null,
  });
});

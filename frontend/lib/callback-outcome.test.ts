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

import { callbackDiagnostics, callbackVerdict, parseCallbackError } from "./callback-outcome.ts";

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

/**
 * Diagnostics for the failure the six live events could not explain: the
 * browser never reached Keycloak's token endpoint, so whatever broke is local.
 * These pin what the event is allowed to say about that - and, just as
 * important, what it must never carry.
 */
class FakeStore {
  private entries: Array<[string, string]>;
  private blocked: boolean;

  constructor(entries: Record<string, string>, blocked = false) {
    this.entries = Object.entries(entries);
    this.blocked = blocked;
  }

  get length(): number {
    if (this.blocked) throw new Error("storage blocked");
    return this.entries.length;
  }

  key(i: number): string | null {
    if (this.blocked) throw new Error("storage blocked");
    return this.entries[i]?.[0] ?? null;
  }

  getItem(): string | null {
    return null;
  }
}

const store = (entries: Record<string, string>, blocked = false) =>
  new FakeStore(entries, blocked) as unknown as Storage;

test("callbackDiagnostics: a live request whose stash survived reads as found", () => {
  assert.deepEqual(
    callbackDiagnostics("?code=abc&state=s1", [store({ "oidc.s1": "{}", unrelated: "x" })]),
    { has_code: true, has_state: true, state_entry: "found", oidc_keys: 1 },
  );
});

test("callbackDiagnostics: a lost stash is distinguishable from unreadable storage", () => {
  assert.deepEqual(
    callbackDiagnostics("?code=abc&state=s1", [store({ "oidc.other": "{}" })]),
    { has_code: true, has_state: true, state_entry: "missing", oidc_keys: 1 },
  );
  assert.deepEqual(callbackDiagnostics("?code=abc&state=s1", [store({}, true), null]), {
    has_code: true,
    has_state: true,
    state_entry: "unreadable",
    oidc_keys: 0,
  });
});

test("callbackDiagnostics: a bare landing with no authorize params says so", () => {
  assert.deepEqual(callbackDiagnostics("", [store({})]), {
    has_code: false,
    has_state: false,
    state_entry: "missing",
    oidc_keys: 0,
  });
});

test("callbackDiagnostics: never carries the single-use code or state values", () => {
  const serialized = JSON.stringify(
    callbackDiagnostics("?code=SECRET_CODE&state=SECRET_STATE", [store({ "oidc.SECRET_STATE": "{}" })]),
  );
  assert.equal(serialized.includes("SECRET_CODE"), false);
  assert.equal(serialized.includes("SECRET_STATE"), false);
});

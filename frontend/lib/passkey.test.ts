/**
 * Unit tests for the passkey-offer freshness gate (lib/passkey.ts).
 *
 * Run with Node's built-in test runner and type stripping:
 *
 *   cd frontend && npm run test:unit
 *
 * What is worth pinning is the one rule the offer lives or dies by: it may
 * appear after a real sign-in and must stay silent when the user merely
 * reopened the console on an existing session. Both cases reach `/callback`
 * with a freshly minted access token, so the only thing telling them apart is
 * `auth_time` — a token missing that claim has to count as "not fresh", or the
 * modal is back to greeting every visit.
 */

import test from "node:test";
import assert from "node:assert/strict";

import { isRecentAuthentication } from "./passkey.ts";

function token(payload: Record<string, unknown>): string {
  const body = Buffer.from(JSON.stringify(payload)).toString("base64url");
  return `header.${body}.signature`;
}

const now = () => Math.floor(Date.now() / 1000);

test("a password login or sign-up counts as fresh", () => {
  assert.equal(isRecentAuthentication(token({ auth_time: now() })), true);
  assert.equal(isRecentAuthentication(token({ auth_time: now() - 30 })), true);
});

test("a session cookie resume does not", () => {
  assert.equal(isRecentAuthentication(token({ auth_time: now() - 3600 })), false);
  assert.equal(isRecentAuthentication(token({ auth_time: now() - 86400 * 3 })), false);
});

test("iat is not a substitute, it is fresh on every resume", () => {
  assert.equal(isRecentAuthentication(token({ iat: now() })), false);
});

test("a token with no usable auth_time stays silent", () => {
  assert.equal(isRecentAuthentication(null), false);
  assert.equal(isRecentAuthentication(""), false);
  assert.equal(isRecentAuthentication("not-a-jwt"), false);
  assert.equal(isRecentAuthentication(token({ auth_time: "1785601785" })), false);
  assert.equal(isRecentAuthentication(token({ auth_time: now() + 600 })), false);
});

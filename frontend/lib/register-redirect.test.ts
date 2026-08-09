/**
 * Unit tests for `readAbandonedRegistration` (lib/register-redirect.ts) --
 * the pure decision function behind the "your last sign-up did not finish"
 * recovery banner on /register.
 *
 * Run with Node's built-in test runner and type stripping:
 *
 *   cd frontend && npm run test:unit
 */

import test from "node:test";
import assert from "node:assert/strict";

import {
  ABANDONED_REGISTRATION_CEILING_MS,
  ABANDONED_REGISTRATION_GRACE_MS,
  isEmailSignupEnabled,
  readAbandonedRegistration,
  registerQueryParams,
} from "./register-redirect.ts";

test("isEmailSignupEnabled: only the literal 'true' turns the flag on", () => {
  assert.equal(isEmailSignupEnabled("true"), true);
  assert.equal(isEmailSignupEnabled("false"), false);
  assert.equal(isEmailSignupEnabled(undefined), false);
  assert.equal(isEmailSignupEnabled(""), false);
  assert.equal(isEmailSignupEnabled("True"), false);
  assert.equal(isEmailSignupEnabled("1"), false);
});

test("registerQueryParams yandex hints the broker and skips prompt=create", () => {
  assert.deepEqual(registerQueryParams("yandex"), { kc_idp_hint: "yandex" });
});

test("registerQueryParams email asks for the registration form", () => {
  assert.deepEqual(registerQueryParams("email"), { prompt: "create" });
});

test("readAbandonedRegistration: null raw returns null", () => {
  assert.equal(readAbandonedRegistration(null, Date.now()), null);
});

test("readAbandonedRegistration: garbage value returns null", () => {
  assert.equal(readAbandonedRegistration("not-a-timestamp", Date.now()), null);
  assert.equal(readAbandonedRegistration("", Date.now()), null);
});

test("readAbandonedRegistration: below the grace window returns null", () => {
  const now = 1_000_000_000_000;
  const startedAt = now - (ABANDONED_REGISTRATION_GRACE_MS - 1);
  const raw = `${startedAt}:email`;
  assert.equal(readAbandonedRegistration(raw, now), null);
});

test("readAbandonedRegistration: inside the recovery window returns method and age", () => {
  const now = 1_000_000_000_000;
  const startedAt = now - (ABANDONED_REGISTRATION_GRACE_MS + 5_000);
  const raw = `${startedAt}:yandex`;
  const result = readAbandonedRegistration(raw, now);
  assert.deepEqual(result, { method: "yandex", ageMs: ABANDONED_REGISTRATION_GRACE_MS + 5_000 });
});

test("readAbandonedRegistration: past the staleness ceiling returns null", () => {
  const now = 1_000_000_000_000;
  const startedAt = now - (ABANDONED_REGISTRATION_CEILING_MS + 1);
  const raw = `${startedAt}:email`;
  assert.equal(readAbandonedRegistration(raw, now), null);
});

test("readAbandonedRegistration: legacy bare-timestamp value still parses, defaults to email", () => {
  const now = 1_000_000_000_000;
  const startedAt = now - (ABANDONED_REGISTRATION_GRACE_MS + 1_000);
  const raw = `${startedAt}`;
  const result = readAbandonedRegistration(raw, now);
  assert.deepEqual(result, { method: "email", ageMs: ABANDONED_REGISTRATION_GRACE_MS + 1_000 });
});

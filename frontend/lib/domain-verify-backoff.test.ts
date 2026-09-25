import assert from "node:assert/strict";
import test from "node:test";
import {
  VERIFY_MAX_ATTEMPTS,
  challengeLabel,
  classifyVerifyFailure,
  manualVerifyCooldown,
  verifyDelayMs,
  verifyExhausted,
} from "./domain-verify-backoff.ts";

test("first check stays fast so a published record is caught quickly", () => {
  assert.equal(verifyDelayMs(0), 10_000);
});

test("delay grows with consecutive failures instead of a flat interval", () => {
  assert.ok(verifyDelayMs(1) > verifyDelayMs(0));
  assert.ok(verifyDelayMs(3) > verifyDelayMs(1));
});

test("delay is capped rather than growing without bound", () => {
  assert.equal(verifyDelayMs(99), verifyDelayMs(VERIFY_MAX_ATTEMPTS));
});

test("whole budget costs far fewer checks than the 29 a real user burned in 8 minutes", () => {
  let total = 0;
  for (let i = 0; i < VERIFY_MAX_ATTEMPTS; i += 1) total += verifyDelayMs(i);
  assert.ok(VERIFY_MAX_ATTEMPTS < 29);
  assert.ok(total > 8 * 60 * 1000);
});

test("polling continues while budget remains and stops once spent", () => {
  assert.equal(verifyExhausted(0), false);
  assert.equal(verifyExhausted(VERIFY_MAX_ATTEMPTS - 1), false);
  assert.equal(verifyExhausted(VERIFY_MAX_ATTEMPTS), true);
});

test("no failure yet classifies as nothing to explain", () => {
  assert.equal(classifyVerifyFailure(null), null);
  assert.equal(classifyVerifyFailure(""), null);
});

test("recognises the resolver error real users actually hit", () => {
  assert.equal(
    classifyVerifyFailure(
      "DNS lookup failed for _dada-verify.run.place: lookup _dada-verify.run.place on 10.96.0.10:53: no such host"
    ),
    "not_published"
  );
});

test("separates a published-but-wrong record from a missing one", () => {
  assert.equal(classifyVerifyFailure("TXT record found but value mismatch"), "wrong_value");
});

test("falls back to a generic kind for anything else", () => {
  assert.equal(classifyVerifyFailure("i/o timeout"), "other");
});

test("strips the zone so the user pastes what most DNS providers ask for", () => {
  assert.equal(challengeLabel("_dada-verify.acme.com", "acme.com"), "_dada-verify");
  assert.equal(challengeLabel("_dada-verify.fanclub.run.place", "fanclub.run.place"), "_dada-verify");
});

test("returns the host unchanged when it is not under the apex", () => {
  assert.equal(challengeLabel("_dada-verify.other.com", "acme.com"), "_dada-verify.other.com");
});

test("manual verify: no prior check ever, so the first click is never blocked", () => {
  const r = manualVerifyCooldown({ attempt: 0, lastVerifyAtMs: null, nowMs: 1_000_000 });
  assert.equal(r.blocked, false);
  assert.equal(r.remainingMs, 0);
});

test("manual verify: a click 3s into the 10s first-attempt window is blocked with 7s left", () => {
  const r = manualVerifyCooldown({ attempt: 0, lastVerifyAtMs: 1_000_000, nowMs: 1_003_000 });
  assert.equal(r.blocked, true);
  assert.equal(r.remainingMs, 7_000);
});

test("manual verify: 43 clicks 0.24s apart (the live incident) let only the first one through", () => {
  let lastVerifyAtMs: number | null = null;
  let attempt = 0;
  let sent = 0;
  let t = 0;
  for (let i = 0; i < 43; i += 1) {
    const r = manualVerifyCooldown({ attempt, lastVerifyAtMs, nowMs: t });
    if (!r.blocked) {
      sent += 1;
      lastVerifyAtMs = t;
      attempt += 1;
    }
    t += 240;
  }
  assert.equal(sent, 1);
});

test("manual verify: a click once the backoff window has fully elapsed is allowed", () => {
  const r = manualVerifyCooldown({ attempt: 0, lastVerifyAtMs: 1_000_000, nowMs: 1_010_000 });
  assert.equal(r.blocked, false);
  assert.equal(r.remainingMs, 0);
});

test("manual verify: 15s after the last check clears attempt 0 (10s wait) but not attempt 3 (60s wait)", () => {
  const early = manualVerifyCooldown({ attempt: 0, lastVerifyAtMs: 0, nowMs: 15_000 });
  const later = manualVerifyCooldown({ attempt: 3, lastVerifyAtMs: 0, nowMs: 15_000 });
  assert.equal(early.blocked, false);
  assert.equal(later.blocked, true);
});

test("manual verify: budget is exhausted at the same threshold the poller uses", () => {
  assert.equal(verifyExhausted(VERIFY_MAX_ATTEMPTS - 1), false);
  assert.equal(verifyExhausted(VERIFY_MAX_ATTEMPTS), true);
});

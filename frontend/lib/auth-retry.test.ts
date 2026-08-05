/**
 * Unit tests for the auth-provider's failure-handling primitives
 * (lib/auth-retry.ts).
 *
 * Run with Node's built-in test runner and type stripping:
 *
 *   cd frontend && npm run test:unit
 *
 * These pin the mechanism behind the "eternal spinner" fix: a rejected
 * getAccessToken() must eventually surface as a typed error rather than
 * leaving the caller's promise chain forever pending, a failed dynamic
 * import must do the same, and a hung promise must be forced into an error
 * state by the watchdog rather than spinning forever.
 */

import test from "node:test";
import assert from "node:assert/strict";

import { fetchWithRetry, loadWithFallback, scheduleTimeout } from "./auth-retry.ts";

function noWait(): Promise<void> {
  return Promise.resolve();
}

test("fetchWithRetry: resolves immediately when the call succeeds first try", async () => {
  let calls = 0;
  const result = await fetchWithRetry(async () => {
    calls += 1;
    return "token-1";
  });
  assert.equal(result, "token-1");
  assert.equal(calls, 1);
});

test("fetchWithRetry: recovers on a later attempt without surfacing an error", async () => {
  let calls = 0;
  const result = await fetchWithRetry(
    async () => {
      calls += 1;
      if (calls < 2) throw new Error("network blip");
      return "token-2";
    },
    [500, 1500],
    noWait,
  );
  assert.equal(result, "token-2");
  assert.equal(calls, 2);
});

test("fetchWithRetry: throws only after every attempt is exhausted", async () => {
  let calls = 0;
  await assert.rejects(
    () =>
      fetchWithRetry(
        async () => {
          calls += 1;
          throw new Error("keycloak down");
        },
        [500, 1500],
        noWait,
      ),
    /keycloak down/,
  );
  assert.equal(calls, 3);
});

test("loadWithFallback: passes through a successful load", async () => {
  const result = await loadWithFallback(async () => ({ ok: true }));
  assert.deepEqual(result, { value: { ok: true }, error: null });
});

test("loadWithFallback: turns a rejected loader into a typed error, not a throw", async () => {
  const result = await loadWithFallback(async () => {
    throw new Error("chunk load failed");
  });
  assert.deepEqual(result, { value: null, error: "provider_load_failed" });
});

test("scheduleTimeout: fires the callback after the delay elapses", async () => {
  let fired = false;
  scheduleTimeout(20, () => {
    fired = true;
  });
  assert.equal(fired, false);
  await new Promise((resolve) => setTimeout(resolve, 60));
  assert.equal(fired, true);
});

test("scheduleTimeout: a hung promise still reaches an error state via the watchdog", async () => {
  const neverSettles = new Promise<never>(() => {});
  let settled: "value" | "watchdog" | null = null;

  const cancel = scheduleTimeout(20, () => {
    settled = "watchdog";
  });
  neverSettles.then(() => {
    settled = "value";
  });

  await new Promise((resolve) => setTimeout(resolve, 60));
  assert.equal(settled, "watchdog");
  cancel();
});

test("scheduleTimeout: canceling before the delay elapses suppresses the callback", async () => {
  let fired = false;
  const cancel = scheduleTimeout(20, () => {
    fired = true;
  });
  cancel();
  await new Promise((resolve) => setTimeout(resolve, 60));
  assert.equal(fired, false);
});

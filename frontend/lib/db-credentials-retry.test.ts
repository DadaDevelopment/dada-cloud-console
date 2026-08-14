import assert from "node:assert/strict";
import test from "node:test";
import {
  CREDENTIALS_PROVISION_RETRY_DELAYS_MS,
  credentialsErrorKind,
  credentialsRetryBudgetMs,
  revealCredentialsWithProvisionRetry,
} from "./db-credentials-retry.ts";

function httpError(status: number): Error & { status: number } {
  return Object.assign(new Error(`status ${status}`), { status });
}

/** Collects the waits instead of performing them, so the schedule runs instantly. */
function recordingDelay(): { waits: number[]; delay: (ms: number) => Promise<void> } {
  const waits: number[] = [];
  return {
    waits,
    delay: async (ms: number) => {
      waits.push(ms);
    },
  };
}

test("classifies reveal failures by status, not by message", () => {
  assert.equal(credentialsErrorKind(404), "notReady");
  assert.equal(credentialsErrorKind(503), "notConfigured");
  assert.equal(credentialsErrorKind(500), "generic");
  assert.equal(credentialsErrorKind(undefined), "generic");
});

test("the retry budget outlasts the measured 148s provisioning gap", () => {
  assert.ok(
    credentialsRetryBudgetMs() > 148_000,
    `budget ${credentialsRetryBudgetMs()}ms must exceed the 148s observed in production`,
  );
  assert.equal(credentialsRetryBudgetMs([1_000, 2_000]), 3_000);
});

test("returns credentials without retrying when the secret is already there", async () => {
  const { waits, delay } = recordingDelay();
  let calls = 0;
  const result = await revealCredentialsWithProvisionRetry(
    async () => {
      calls++;
      return { username: "app" };
    },
    { delay },
  );
  assert.deepEqual(result, { ok: true, value: { username: "app" }, retries: 0 });
  assert.equal(calls, 1);
  assert.deepEqual(waits, []);
});

test("keeps retrying a still-provisioning database and reports progress", async () => {
  const { waits, delay } = recordingDelay();
  const progress: number[] = [];
  let calls = 0;
  const result = await revealCredentialsWithProvisionRetry(
    async () => {
      calls++;
      if (calls < 4) throw httpError(404);
      return { username: "app" };
    },
    {
      delay,
      onRetry: (p) => progress.push(p.attempt),
    },
  );
  assert.equal(result.ok, true);
  assert.equal(calls, 4);
  assert.equal((result as { retries: number }).retries, 3);
  assert.deepEqual(waits, CREDENTIALS_PROVISION_RETRY_DELAYS_MS.slice(0, 3));
  assert.deepEqual(progress, [1, 2, 3]);
});

test("gives up on a status that waiting cannot fix", async () => {
  const { waits, delay } = recordingDelay();
  let calls = 0;
  const result = await revealCredentialsWithProvisionRetry(
    async () => {
      calls++;
      throw httpError(503);
    },
    { delay },
  );
  assert.equal(result.ok, false);
  assert.equal((result as { kind: string }).kind, "notConfigured");
  assert.equal(calls, 1);
  assert.deepEqual(waits, []);
});

test("exhausts the schedule and reports the transient kind", async () => {
  const { waits, delay } = recordingDelay();
  let calls = 0;
  const result = await revealCredentialsWithProvisionRetry(
    async () => {
      calls++;
      throw httpError(404);
    },
    { delay },
  );
  assert.equal(result.ok, false);
  assert.equal((result as { kind: string }).kind, "notReady");
  assert.equal(calls, CREDENTIALS_PROVISION_RETRY_DELAYS_MS.length + 1);
  assert.deepEqual(waits, CREDENTIALS_PROVISION_RETRY_DELAYS_MS);
});

test("stops the loop when the caller is gone", async () => {
  const { delay } = recordingDelay();
  let calls = 0;
  let cancelled = false;
  const result = await revealCredentialsWithProvisionRetry(
    async () => {
      calls++;
      cancelled = true;
      throw httpError(404);
    },
    { delay, isCancelled: () => cancelled },
  );
  assert.equal(result.ok, false);
  assert.equal(calls, 1);
});

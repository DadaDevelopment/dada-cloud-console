/**
 * Unit tests for the recovery-prompt dismissal and routing logic
 * (lib/recovery-prompt.ts).
 *
 * Run with Node's built-in test runner and type stripping:
 *
 *   cd frontend && npm run test:unit
 */

import test from "node:test";
import assert from "node:assert/strict";
import type { RecoveryPrompt } from "./types.ts";

class FakeStorage {
  private data = new Map<string, string>();
  failWrites = false;

  getItem(key: string): string | null {
    return this.data.get(key) ?? null;
  }

  setItem(key: string, value: string): void {
    if (this.failWrites) throw new Error("quota exceeded");
    this.data.set(key, value);
  }
}

function installWindow(): { storage: FakeStorage } {
  const storage = new FakeStorage();
  const win = { localStorage: storage };
  (globalThis as Record<string, unknown>).window = win;
  return { storage };
}

const {
  isRecoveryPromptDismissed,
  dismissRecoveryPrompt,
  shouldShowRecoveryPrompt,
  recoveryPromptHref,
} = await (async () => {
  installWindow();
  return import("./recovery-prompt.ts");
})();

const INSTALL_PROMPT: RecoveryPrompt = {
  kind: "solution_install_env_failed",
  failed_at: "2026-08-19T04:11:38Z",
  fixed_at: "2026-08-19T11:57:00Z",
  project_id: "proj-1",
  environment_id: "env-1",
  resource_name: "nextjs-app",
};

const PAYMENT_PROMPT: RecoveryPrompt = {
  kind: "payment_recurring_forbidden",
  failed_at: "2026-08-10T00:00:00Z",
  fixed_at: "2026-08-11T00:00:00Z",
  project_id: "proj-2",
  environment_id: "env-2",
  resource_name: "checkout",
};

test("a null prompt never shows", () => {
  assert.equal(shouldShowRecoveryPrompt(null), false);
});

test("an undismissed prompt shows", () => {
  installWindow();
  assert.equal(shouldShowRecoveryPrompt(INSTALL_PROMPT), true);
});

test("dismissing hides the prompt for that exact kind + failed_at only", () => {
  installWindow();
  assert.equal(shouldShowRecoveryPrompt(INSTALL_PROMPT), true);

  dismissRecoveryPrompt(INSTALL_PROMPT.kind, INSTALL_PROMPT.failed_at);

  assert.equal(isRecoveryPromptDismissed(INSTALL_PROMPT.kind, INSTALL_PROMPT.failed_at), true);
  assert.equal(shouldShowRecoveryPrompt(INSTALL_PROMPT), false);
  assert.equal(shouldShowRecoveryPrompt(PAYMENT_PROMPT), true);
});

test("a new failure of the same kind is a fresh key, unaffected by an old dismissal", () => {
  installWindow();
  dismissRecoveryPrompt(INSTALL_PROMPT.kind, INSTALL_PROMPT.failed_at);
  assert.equal(shouldShowRecoveryPrompt(INSTALL_PROMPT), false);

  const laterFailure: RecoveryPrompt = { ...INSTALL_PROMPT, failed_at: "2026-08-20T00:00:00Z" };
  assert.equal(shouldShowRecoveryPrompt(laterFailure), true);
});

test("a write storage rejects leaves the prompt reappearing rather than crashing", () => {
  const { storage } = installWindow();
  storage.failWrites = true;

  dismissRecoveryPrompt(INSTALL_PROMPT.kind, INSTALL_PROMPT.failed_at);

  assert.equal(isRecoveryPromptDismissed(INSTALL_PROMPT.kind, INSTALL_PROMPT.failed_at), false);
  assert.equal(shouldShowRecoveryPrompt(INSTALL_PROMPT), true);
});

test("no window at all fails closed: dismissed reads true, prompt never shows", () => {
  delete (globalThis as Record<string, unknown>).window;
  assert.equal(isRecoveryPromptDismissed(INSTALL_PROMPT.kind, INSTALL_PROMPT.failed_at), true);
  assert.equal(shouldShowRecoveryPrompt(INSTALL_PROMPT), false);
});

test("install kind routes back to the apps page scoped to the failed environment", () => {
  assert.equal(recoveryPromptHref(INSTALL_PROMPT), "/projects/proj-1/apps?envId=env-1");
});

test("payment kind routes to the project's billing page", () => {
  assert.equal(recoveryPromptHref(PAYMENT_PROMPT), "/projects/proj-2/billing");
});

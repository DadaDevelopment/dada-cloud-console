/**
 * Unit tests for the live-URL confirmation banner logic (lib/app-live-banner.ts).
 *
 * Run with Node's built-in test runner and type stripping:
 *
 *   cd frontend && npm run test:unit
 */

import test from "node:test";
import assert from "node:assert/strict";

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

const { isAppLive, shouldShowLiveBanner, isLiveBannerDismissed, dismissLiveBanner, subscribeLiveBannerDismissal } = await (async () => {
  installWindow();
  return import("./app-live-banner.ts");
})();

const LIVE = {
  projectId: "p1",
  appName: "demo",
  url: "https://demo.dada-tuda.ru",
  phase: "Ready",
  urlStatus: "active",
  urlReason: null as string | null,
};

test("no url means not live regardless of phase or status", () => {
  assert.equal(isAppLive({ ...LIVE, url: null }), false);
  assert.equal(isAppLive({ ...LIVE, url: "" }), false);
});

test("phase must be Ready or Running, case-insensitively", () => {
  assert.equal(isAppLive({ ...LIVE, phase: "ready" }), true);
  assert.equal(isAppLive({ ...LIVE, phase: "RUNNING" }), true);
  assert.equal(isAppLive({ ...LIVE, phase: "Pending" }), false);
  assert.equal(isAppLive({ ...LIVE, phase: null }), false);
});

test("url_status must be active, not merely present or unknown", () => {
  assert.equal(isAppLive({ ...LIVE, urlStatus: "pending" }), false);
  assert.equal(isAppLive({ ...LIVE, urlStatus: "failed" }), false);
  assert.equal(isAppLive({ ...LIVE, urlStatus: "unknown" }), false);
  assert.equal(isAppLive({ ...LIVE, urlStatus: null }), false);
});

test("awaiting_first_deploy is never live even if url_status somehow says active", () => {
  assert.equal(isAppLive({ ...LIVE, urlReason: "awaiting_first_deploy" }), false);
});

test("a fully live app with no dismissal is shown", () => {
  installWindow();
  assert.equal(isAppLive(LIVE), true);
  assert.equal(shouldShowLiveBanner(LIVE), true);
});

test("dismissing hides the banner for that project+app only", () => {
  installWindow();
  assert.equal(shouldShowLiveBanner(LIVE), true);

  dismissLiveBanner(LIVE.projectId, LIVE.appName);

  assert.equal(isLiveBannerDismissed(LIVE.projectId, LIVE.appName), true);
  assert.equal(shouldShowLiveBanner(LIVE), false);
  assert.equal(shouldShowLiveBanner({ ...LIVE, appName: "other" }), true);
});

test("a write storage rejects leaves the banner reappearing rather than crashing", () => {
  const { storage } = installWindow();
  storage.failWrites = true;

  dismissLiveBanner(LIVE.projectId, LIVE.appName);

  assert.equal(isLiveBannerDismissed(LIVE.projectId, LIVE.appName), false);
  assert.equal(shouldShowLiveBanner(LIVE), true);
});

test("no window at all fails closed: dismissed reads true, banner never shows", () => {
  delete (globalThis as Record<string, unknown>).window;
  assert.equal(isLiveBannerDismissed(LIVE.projectId, LIVE.appName), true);
  assert.equal(shouldShowLiveBanner(LIVE), false);
});

test("dismissal notifies subscribers so the rendered banner re-reads storage", () => {
  installWindow();
  let calls = 0;
  const unsubscribe = subscribeLiveBannerDismissal(() => {
    calls += 1;
  });

  dismissLiveBanner(LIVE.projectId, LIVE.appName);
  assert.equal(calls, 1);

  unsubscribe();
  dismissLiveBanner(LIVE.projectId, "another");
  assert.equal(calls, 1);
});

test("a failed write still notifies, so the banner re-reads and stays honest", () => {
  const { storage } = installWindow();
  storage.failWrites = true;
  let calls = 0;
  const unsubscribe = subscribeLiveBannerDismissal(() => {
    calls += 1;
  });

  dismissLiveBanner(LIVE.projectId, LIVE.appName);

  assert.equal(calls, 1);
  assert.equal(isLiveBannerDismissed(LIVE.projectId, LIVE.appName), false);
  unsubscribe();
});

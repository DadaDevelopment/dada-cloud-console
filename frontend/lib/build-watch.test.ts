/**
 * Unit tests for the tracked-build change signal (lib/build-watch.ts).
 *
 * Run with Node's built-in test runner and type stripping:
 *
 *   cd frontend && npm run test:unit
 *
 * The one thing worth pinning here is the reason the build-finish notice was
 * unreachable in production for its whole life: `BuildWatcher` mounts once,
 * inside the console layout that survives every client-side navigation, and
 * read the tracked list exactly once at that mount. Every caller of
 * `trackBuildStart` runs later, in that same tab, with no reload -- and the
 * browser's `storage` event deliberately does not fire in the tab that wrote.
 * So the watcher never saw the build the user had just triggered.
 *
 * These tests therefore assert the contract the watcher subscribes to: a
 * successful write announces itself on BUILD_TRACK_EVENT in the writing tab,
 * a failed write stays silent, and a listener re-reading storage on that
 * event observes the new entry.
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

function installWindow(): { storage: FakeStorage; target: EventTarget } {
  const storage = new FakeStorage();
  const target = new EventTarget();
  const win = {
    localStorage: storage,
    addEventListener: target.addEventListener.bind(target),
    removeEventListener: target.removeEventListener.bind(target),
    dispatchEvent: target.dispatchEvent.bind(target),
  };
  (globalThis as Record<string, unknown>).window = win;
  return { storage, target };
}

const { BUILD_TRACK_EVENT, readTrackedBuilds, trackBuildStart, untrackBuild } = await (async () => {
  installWindow();
  return import("./build-watch.ts");
})();

const ENTRY = {
  projectId: "p1",
  envId: "e1",
  appName: "demo",
  buildId: "b1",
};

test("a build tracked after mount announces itself in the same tab", () => {
  installWindow();
  const seen: string[][] = [];
  window.addEventListener(BUILD_TRACK_EVENT, () => {
    seen.push(readTrackedBuilds().map((b) => b.buildId));
  });

  trackBuildStart(ENTRY);

  assert.deepEqual(seen, [["b1"]]);
});

test("a listener re-reading on the event observes the new entry", () => {
  installWindow();
  let tracked = readTrackedBuilds();
  assert.deepEqual(tracked, []);
  window.addEventListener(BUILD_TRACK_EVENT, () => {
    tracked = readTrackedBuilds();
  });

  trackBuildStart(ENTRY);
  assert.deepEqual(
    tracked.map((b) => b.buildId),
    ["b1"],
  );

  trackBuildStart({ ...ENTRY, buildId: "b2" });
  assert.deepEqual(
    tracked.map((b) => b.buildId),
    ["b1", "b2"],
  );

  untrackBuild("b1");
  assert.deepEqual(
    tracked.map((b) => b.buildId),
    ["b2"],
  );
});

test("a write that storage rejected stays silent", () => {
  const { storage } = installWindow();
  storage.failWrites = true;
  let announced = 0;
  window.addEventListener(BUILD_TRACK_EVENT, () => {
    announced += 1;
  });

  trackBuildStart(ENTRY);

  assert.equal(announced, 0);
  assert.deepEqual(readTrackedBuilds(), []);
});

test("entries older than the freshness window never resurface as new", () => {
  installWindow();
  const stale = [{ ...ENTRY, startedAt: Date.now() - 3 * 60 * 60 * 1000 }];
  window.localStorage.setItem("dada_tracked_builds", JSON.stringify(stale));

  assert.deepEqual(readTrackedBuilds(), []);
});

/**
 * The deploy badge is the one entry point whose visitor has not signed up for
 * anything yet, so it tracks the build without raising a permission dialog.
 * Both halves matter: suppressing the prompt must not also suppress tracking,
 * or the badge visitor is back to learning nothing about their build.
 */
function installNotificationSpy(): { asked: () => number } {
  let asked = 0;
  (window as unknown as Record<string, unknown>).Notification = {
    permission: "default",
    requestPermission: () => {
      asked += 1;
      return Promise.resolve("default");
    },
  };
  return { asked: () => asked };
}

test("an entry point that opts out of the prompt still gets tracked", () => {
  installWindow();
  const spy = installNotificationSpy();

  trackBuildStart(ENTRY, { requestNotifyPermission: false });

  assert.equal(spy.asked(), 0);
  assert.deepEqual(
    readTrackedBuilds().map((b) => b.buildId),
    [ENTRY.buildId]
  );
});

test("console entry points still raise the prompt by default", () => {
  installWindow();
  const spy = installNotificationSpy();

  trackBuildStart(ENTRY);

  assert.equal(spy.asked(), 1);
});

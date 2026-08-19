import assert from "node:assert/strict";
import test from "node:test";
import {
  platformHealthAgeSeconds,
  platformHealthIsStale,
  platformHealthState,
} from "./platform-health.ts";

test("blind wins over an empty unhealthy list when the check never ran", () => {
  assert.equal(platformHealthState({ observed: false, unhealthy: [] }), "blind");
  assert.equal(
    platformHealthState({ observed: false, unhealthy: [{}] as never[] }),
    "blind"
  );
});

test("observed with no unhealthy rows reads as healthy", () => {
  assert.equal(platformHealthState({ observed: true, unhealthy: [] }), "healthy");
});

test("observed with unhealthy rows reads as unhealthy", () => {
  assert.equal(platformHealthState({ observed: true, unhealthy: [{}] as never[] }), "unhealthy");
});

test("computes elapsed seconds since checked_at", () => {
  const now = new Date("2026-08-19T19:10:00Z").getTime();
  assert.equal(platformHealthAgeSeconds("2026-08-19T19:00:00Z", now), 600);
  assert.equal(platformHealthAgeSeconds("2026-08-19T19:10:00Z", now), 0);
});

test("an unparsable timestamp counts as infinitely stale, not fresh", () => {
  assert.equal(platformHealthAgeSeconds("", Date.now()), Number.POSITIVE_INFINITY);
  assert.equal(platformHealthIsStale("", Date.now()), true);
});

test("flags a snapshot older than the stale window", () => {
  const now = new Date("2026-08-19T19:10:00Z").getTime();
  assert.equal(platformHealthIsStale("2026-08-19T19:09:00Z", now), false);
  assert.equal(platformHealthIsStale("2026-08-19T19:00:00Z", now), true);
});

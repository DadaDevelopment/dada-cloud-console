import assert from "node:assert/strict";
import test from "node:test";

import { evaluateVolumeUsage, formatCount } from "./volume-usage.ts";

test("evaluateVolumeUsage: inodes worse than bytes drives crit severity off inodes, and bytes stay ok", () => {
  const view = evaluateVolumeUsage({ ratio: 0.74, inodes_used: 1310720, inodes_total: 1310720, inodes_ratio: 1 });
  assert.equal(view.hasInodes, true);
  assert.equal(view.bytesSeverity, "ok");
  assert.equal(view.inodesSeverity, "crit");
  assert.equal(view.overallSeverity, "crit");
  assert.equal(view.displayRatio, 1);
});

test("evaluateVolumeUsage: bytes worse than inodes keeps today's byte-driven severity, inodes stay quiet", () => {
  const view = evaluateVolumeUsage({ ratio: 0.9, inodes_used: 100, inodes_total: 1310720, inodes_ratio: 0.5 });
  assert.equal(view.hasInodes, true);
  assert.equal(view.bytesSeverity, "warn");
  assert.equal(view.inodesSeverity, "ok");
  assert.equal(view.overallSeverity, "warn");
  assert.equal(view.displayRatio, 0.9);
});

test("evaluateVolumeUsage: missing inode fields report hasInodes=false, never a fake 0%", () => {
  const view = evaluateVolumeUsage({ ratio: 0.42 });
  assert.equal(view.hasInodes, false);
  assert.equal(view.inodesRatio, null);
  assert.equal(view.inodesSeverity, "ok");
  assert.equal(view.overallSeverity, "ok");
  assert.equal(view.displayRatio, 0.42);
});

test("evaluateVolumeUsage: partial inode payload (ratio without total) is treated as absent", () => {
  const view = evaluateVolumeUsage({ ratio: 0.3, inodes_ratio: 0.99 });
  assert.equal(view.hasInodes, false);
  assert.equal(view.inodesRatio, null);
});

test("evaluateVolumeUsage: thresholds match the shipped bytes behavior (0.85 warn, 0.95 crit)", () => {
  assert.equal(evaluateVolumeUsage({ ratio: 0.84 }).bytesSeverity, "ok");
  assert.equal(evaluateVolumeUsage({ ratio: 0.85 }).bytesSeverity, "warn");
  assert.equal(evaluateVolumeUsage({ ratio: 0.94 }).bytesSeverity, "warn");
  assert.equal(evaluateVolumeUsage({ ratio: 0.95 }).bytesSeverity, "crit");
});

test("formatCount: groups thousands with a plain ASCII space, never a locale non-breaking space", () => {
  assert.equal(formatCount(1310720), "1 310 720");
  assert.equal(formatCount(0), "0");
  assert.equal(formatCount(999), "999");
  assert.equal(formatCount(1000), "1 000");
  assert.equal(formatCount(42), "42");
  const grouped = formatCount(1310720);
  assert.equal(grouped.includes(String.fromCharCode(160)), false);
});

test("formatCount: rounds fractional input and rejects negative/non-finite as a dash", () => {
  assert.equal(formatCount(1234.6), "1 235");
  assert.equal(formatCount(-5), "-");
  assert.equal(formatCount(NaN), "-");
  assert.equal(formatCount(Infinity), "-");
});

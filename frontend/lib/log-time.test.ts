import assert from "node:assert/strict";
import test from "node:test";
import { formatLogTime } from "./log-time.ts";

test("formats log timestamps in the browser timezone", () => {
  assert.equal(formatLogTime("2026-08-04T19:01:33.312Z", "Europe/Moscow"), "22:01:33");
});

test("preserves an empty or invalid timestamp", () => {
  assert.equal(formatLogTime(""), "");
  assert.equal(formatLogTime("not-a-timestamp"), "not-a-timestamp");
});

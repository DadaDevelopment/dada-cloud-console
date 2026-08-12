import assert from "node:assert/strict";
import test from "node:test";
import {
  feedbackStatusBadgeClass,
  feedbackStatusLabelKey,
  feedbackAgeParts,
  feedbackFirstLine,
} from "./feedback-status.ts";

test("maps known statuses to their badge class", () => {
  assert.equal(feedbackStatusBadgeClass("new"), "bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-400");
  assert.equal(feedbackStatusBadgeClass("in_progress"), "bg-blue-50 text-blue-700 dark:bg-blue-950/40 dark:text-blue-400");
  assert.equal(feedbackStatusBadgeClass("resolved"), "bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400");
});

test("unknown status badge falls back to resolved's neutral class", () => {
  assert.equal(feedbackStatusBadgeClass("some-future-status"), feedbackStatusBadgeClass("resolved"));
});

test("maps known statuses to their i18n label key", () => {
  assert.equal(feedbackStatusLabelKey("new"), "feedback.mine.status.new");
  assert.equal(feedbackStatusLabelKey("in_progress"), "feedback.mine.status.inProgress");
  assert.equal(feedbackStatusLabelKey("resolved"), "feedback.mine.status.resolved");
});

test("unknown status label falls back to resolved's key", () => {
  assert.equal(feedbackStatusLabelKey("archived"), "feedback.mine.status.resolved");
});

test("age under 48h reports whole hours", () => {
  const now = Date.parse("2026-08-12T12:00:00Z");
  assert.deepEqual(feedbackAgeParts("2026-08-12T09:30:00Z", now), { unit: "hours", count: 2 });
  assert.deepEqual(feedbackAgeParts("2026-08-12T12:00:00Z", now), { unit: "hours", count: 0 });
});

test("age at or beyond 48h reports whole days", () => {
  const now = Date.parse("2026-08-12T12:00:00Z");
  assert.deepEqual(feedbackAgeParts("2026-08-10T12:00:00Z", now), { unit: "days", count: 2 });
  assert.deepEqual(feedbackAgeParts("2026-08-01T00:00:00Z", now), { unit: "days", count: 11 });
});

test("age never goes negative for a clock-skewed created_at", () => {
  const now = Date.parse("2026-08-12T12:00:00Z");
  assert.deepEqual(feedbackAgeParts("2026-08-12T13:00:00Z", now), { unit: "hours", count: 0 });
});

test("malformed created_at is treated as just-now rather than throwing", () => {
  const now = Date.parse("2026-08-12T12:00:00Z");
  assert.deepEqual(feedbackAgeParts("not-a-date", now), { unit: "hours", count: 0 });
});

test("first line trims and skips leading blank lines", () => {
  assert.equal(feedbackFirstLine("Hello world\nmore detail"), "Hello world");
  assert.equal(feedbackFirstLine("\n  \nActual first line\nsecond"), "Actual first line");
  assert.equal(feedbackFirstLine("   single line, padded   "), "single line, padded");
});

test("first line of an all-blank message is empty", () => {
  assert.equal(feedbackFirstLine("\n \n"), "");
});

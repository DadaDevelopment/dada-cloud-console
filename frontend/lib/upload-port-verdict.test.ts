import assert from "node:assert/strict";
import test from "node:test";

import {
  classifyUploadPortError,
  needsPortVerdict,
  parseUploadPort,
} from "./upload-port-verdict.ts";

test("a detected web port skips the verdict step", () => {
  assert.equal(needsPortVerdict({ framework: "next", port: 3000 }), false);
});

test("port 0 (worker detection) needs the verdict step", () => {
  assert.equal(needsPortVerdict({ framework: "", port: 0 }), true);
});

test("a missing detected object needs the verdict step", () => {
  assert.equal(needsPortVerdict(null), true);
  assert.equal(needsPortVerdict(undefined), true);
});

test("a negative or non-finite port needs the verdict step", () => {
  assert.equal(needsPortVerdict({ framework: "next", port: -1 }), true);
  assert.equal(needsPortVerdict({ framework: "next", port: NaN }), true);
});

test("parseUploadPort accepts the full valid range", () => {
  assert.equal(parseUploadPort("1"), 1);
  assert.equal(parseUploadPort("65535"), 65535);
  assert.equal(parseUploadPort("3000"), 3000);
});

test("parseUploadPort rejects out-of-range, non-integer and empty input", () => {
  assert.equal(parseUploadPort("0"), null);
  assert.equal(parseUploadPort("65536"), null);
  assert.equal(parseUploadPort("3000.5"), null);
  assert.equal(parseUploadPort(""), null);
  assert.equal(parseUploadPort("abc"), null);
});

test("classifyUploadPortError flags only the invalid_port code as a range error", () => {
  const r = classifyUploadPortError({ code: "invalid_port", message: "out of range" });
  assert.equal(r.isRangeError, true);
});

test("classifyUploadPortError passes through the backend's own message for any other code", () => {
  const r = classifyUploadPortError({ code: "app_is_worker", message: "this app has no web port" });
  assert.equal(r.isRangeError, false);
  assert.equal(r.message, "this app has no web port");
});

test("classifyUploadPortError tolerates a non-error-shaped value", () => {
  const r = classifyUploadPortError("boom");
  assert.equal(r.isRangeError, false);
  assert.equal(r.message, "");
});

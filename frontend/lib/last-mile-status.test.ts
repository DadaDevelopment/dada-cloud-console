import assert from "node:assert/strict";
import test from "node:test";
import { evaluateLastMile } from "./last-mile-status.ts";

test("no verdict when the probe never ran", () => {
  assert.equal(evaluateLastMile(undefined), null);
  assert.equal(evaluateLastMile(null), null);
  assert.equal(evaluateLastMile({}), null);
  assert.equal(evaluateLastMile({ http_status: 404 }), null);
  assert.equal(evaluateLastMile({ http_checked_at: "2026-08-15T10:00:00Z" }), null);
});

test("no verdict when the address is serving (2xx/3xx)", () => {
  assert.equal(
    evaluateLastMile({ http_status: 200, http_checked_at: "2026-08-15T10:00:00Z" }),
    null,
  );
  assert.equal(
    evaluateLastMile({ http_status: 301, http_checked_at: "2026-08-15T10:00:00Z" }),
    null,
  );
  assert.equal(
    evaluateLastMile({ http_status: 399, http_checked_at: "2026-08-15T10:00:00Z" }),
    null,
  );
});

test("dead verdict for a 4xx/5xx response, carrying the exact status and reason", () => {
  assert.deepEqual(
    evaluateLastMile({ http_status: 404, http_reason: "status_404", http_checked_at: "2026-08-15T10:00:00Z" }),
    { status: 404, reason: "status_404", checkedAt: "2026-08-15T10:00:00Z" },
  );
  assert.deepEqual(
    evaluateLastMile({ http_status: 502, http_reason: "status_502", http_checked_at: "2026-08-15T10:00:00Z" }),
    { status: 502, reason: "status_502", checkedAt: "2026-08-15T10:00:00Z" },
  );
});

test("dead verdict for status 0 (no response at all), reason optional", () => {
  assert.deepEqual(
    evaluateLastMile({ http_status: 0, http_reason: "timeout", http_checked_at: "2026-08-15T10:00:00Z" }),
    { status: 0, reason: "timeout", checkedAt: "2026-08-15T10:00:00Z" },
  );
  assert.deepEqual(
    evaluateLastMile({ http_status: 0, http_checked_at: "2026-08-15T10:00:00Z" }),
    { status: 0, reason: "", checkedAt: "2026-08-15T10:00:00Z" },
  );
});

import assert from "node:assert/strict";
import test from "node:test";
import { evaluateLastMile, isDeadHTTPStatus } from "./last-mile-status.ts";

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

test("dead verdict for a 502/503/504 gateway response, carrying the exact status and reason", () => {
  assert.deepEqual(
    evaluateLastMile({ http_status: 502, http_reason: "status_502", http_checked_at: "2026-08-15T10:00:00Z" }),
    { status: 502, reason: "status_502", checkedAt: "2026-08-15T10:00:00Z" },
  );
  assert.deepEqual(
    evaluateLastMile({ http_status: 503, http_reason: "status_503", http_checked_at: "2026-08-15T10:00:00Z" }),
    { status: 503, reason: "status_503", checkedAt: "2026-08-15T10:00:00Z" },
  );
  assert.deepEqual(
    evaluateLastMile({ http_status: 504, http_reason: "status_504", http_checked_at: "2026-08-15T10:00:00Z" }),
    { status: 504, reason: "status_504", checkedAt: "2026-08-15T10:00:00Z" },
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

/**
 * TestOverviewLiveURLsAppErrorIsNotDeadButGatewayErrorIs's frontend twin: a
 * 404 the application itself produced (telemost-bot: no route at "/", 200
 * on /health) must never read as "last mile dead" -- that used to be the
 * exact bug, folding a healthy headless app in with n8n's hash-domain
 * ingress answering 503 with no backend Service behind it at all.
 */
test("no verdict for a 404 the app itself answered (telemost-bot shape)", () => {
  assert.equal(
    evaluateLastMile({ http_status: 404, http_reason: "status_404", http_checked_at: "2026-08-15T10:00:00Z" }),
    null,
  );
});

test("dead verdict for a 503 from a proxy with no backend (n8n hash-domain shape)", () => {
  assert.deepEqual(
    evaluateLastMile({ http_status: 503, http_reason: "status_503", http_checked_at: "2026-08-15T10:00:00Z" }),
    { status: 503, reason: "status_503", checkedAt: "2026-08-15T10:00:00Z" },
  );
});

test("isDeadHTTPStatus: only 0/502/503/504 are dead, every other status is the app answering", () => {
  assert.equal(isDeadHTTPStatus(0), true);
  assert.equal(isDeadHTTPStatus(502), true);
  assert.equal(isDeadHTTPStatus(503), true);
  assert.equal(isDeadHTTPStatus(504), true);
  assert.equal(isDeadHTTPStatus(404), false);
  assert.equal(isDeadHTTPStatus(401), false);
  assert.equal(isDeadHTTPStatus(403), false);
  assert.equal(isDeadHTTPStatus(500), false);
  assert.equal(isDeadHTTPStatus(200), false);
});

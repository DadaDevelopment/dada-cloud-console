import assert from "node:assert/strict";
import test from "node:test";
import { evaluateLastMile, isDeadHTTPStatus, isDeadLastMile } from "./last-mile-status.ts";

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

/**
 * The emitter half of the same boundary, measured on production
 * 2026-08-15: fonbet-value's own container answered 503 with a JSON body
 * listing its readiness blockers, while ingress-nginx answered the exact
 * same status class for n8n, whose Service no longer exists. gitops-agent
 * records the difference in http_reason (app_status_<code> vs
 * status_<code>), so the banner must stop calling a talking app dead while
 * still calling a backend-less route dead.
 */
test("no dead verdict for a 5xx the app itself authored (fonbet-value shape)", () => {
  assert.equal(
    evaluateLastMile({ http_status: 503, http_reason: "app_status_503", http_checked_at: "2026-08-15T10:00:00Z" }),
    null,
  );
  assert.equal(
    evaluateLastMile({ http_status: 502, http_reason: "app_status_502", http_checked_at: "2026-08-15T10:00:00Z" }),
    null,
  );
});

test("a worker has no HTTP surface, so a leftover domain's 502 is not a verdict (fanvk shape)", () => {
  assert.equal(
    evaluateLastMile({
      worker: true,
      http_status: 502,
      http_reason: "status_502",
      http_checked_at: "2026-08-15T10:00:00Z",
    }),
    null,
  );
});

test("http_status 0 stays dead whatever the reason says: no answer carries no authorship", () => {
  assert.deepEqual(
    evaluateLastMile({ http_status: 0, http_reason: "app_status_0", http_checked_at: "2026-08-15T10:00:00Z" }),
    { status: 0, reason: "app_status_0", checkedAt: "2026-08-15T10:00:00Z" },
  );
});

test("isDeadLastMile: the reason names the author, the status alone does not", () => {
  assert.equal(isDeadLastMile(503, "status_503"), true);
  assert.equal(isDeadLastMile(503, "app_status_503"), false);
  assert.equal(isDeadLastMile(502, "app_status_502"), false);
  assert.equal(isDeadLastMile(504, ""), true);
  assert.equal(isDeadLastMile(0, "app_status_0"), true);
  assert.equal(isDeadLastMile(404, "status_404"), false);
});

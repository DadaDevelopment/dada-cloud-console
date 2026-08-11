import assert from "node:assert/strict";
import test from "node:test";
import { normalizeAppUrlStatus, appUrlReasonMessageKey } from "./app-url-status.ts";

test("normalizes known url_status values as-is", () => {
  assert.equal(normalizeAppUrlStatus("active"), "active");
  assert.equal(normalizeAppUrlStatus("pending"), "pending");
  assert.equal(normalizeAppUrlStatus("failed"), "failed");
});

test("treats missing, unknown, or malformed url_status as unknown", () => {
  assert.equal(normalizeAppUrlStatus(undefined), "unknown");
  assert.equal(normalizeAppUrlStatus(null), "unknown");
  assert.equal(normalizeAppUrlStatus(""), "unknown");
  assert.equal(normalizeAppUrlStatus("Active"), "unknown");
  assert.equal(normalizeAppUrlStatus("some-future-status"), "unknown");
});

test("maps known url_reason codes to their message key", () => {
  assert.equal(appUrlReasonMessageKey("dns_not_pointed"), "apps.url.reason.dns_not_pointed");
  assert.equal(appUrlReasonMessageKey("cert_pending"), "apps.url.reason.cert_pending");
  assert.equal(appUrlReasonMessageKey("attach_timeout"), "apps.url.reason.attach_timeout");
  assert.equal(appUrlReasonMessageKey("route_missing"), "apps.url.reason.route_missing");
  assert.equal(appUrlReasonMessageKey("app_deleted"), "apps.url.reason.app_deleted");
  assert.equal(appUrlReasonMessageKey("awaiting_first_deploy"), "apps.url.reason.awaiting_first_deploy");
});

test("returns null for absent reason, but never hides an unknown one", () => {
  assert.equal(appUrlReasonMessageKey(undefined), null);
  assert.equal(appUrlReasonMessageKey(null), null);
  assert.equal(appUrlReasonMessageKey(""), null);
  assert.equal(appUrlReasonMessageKey("some_new_backend_code"), null);
});

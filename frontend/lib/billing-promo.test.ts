/**
 * Unit tests for the promo-redeem error mapper (lib/billing-promo.ts).
 *
 * Run with Node's built-in test runner and type stripping:
 *
 *   cd frontend && npm run test:unit
 */

import test from "node:test";
import assert from "node:assert/strict";
import { promoErrorMessageKey, normalizePromoCode } from "./billing-promo.ts";

test("each known backend error code maps to its own message key", () => {
  assert.equal(promoErrorMessageKey("promo_code_not_found"), "billing.promo.error.promo_code_not_found");
  assert.equal(promoErrorMessageKey("promo_code_expired"), "billing.promo.error.promo_code_expired");
  assert.equal(promoErrorMessageKey("promo_code_exhausted"), "billing.promo.error.promo_code_exhausted");
  assert.equal(promoErrorMessageKey("promo_already_redeemed"), "billing.promo.error.promo_already_redeemed");
  assert.equal(promoErrorMessageKey("promo_org_unresolved"), "billing.promo.error.promo_org_unresolved");
  assert.equal(promoErrorMessageKey("promo_code_required"), "billing.promo.error.promo_code_required");
});

test("an unrecognized code falls back to the generic message, never blank", () => {
  assert.equal(promoErrorMessageKey("some_future_backend_code"), "billing.promo.error.generic");
});

test("a missing code (e.g. a network error with no body) falls back to the generic message", () => {
  assert.equal(promoErrorMessageKey(undefined), "billing.promo.error.generic");
  assert.equal(promoErrorMessageKey(null), "billing.promo.error.generic");
});

test("normalizePromoCode trims and upper-cases exactly like the backend", () => {
  assert.equal(normalizePromoCode("  studstartup  "), "STUDSTARTUP");
  assert.equal(normalizePromoCode("StudStartup"), "STUDSTARTUP");
});

test("normalizePromoCode of blank input is empty, matching the required-code gate", () => {
  assert.equal(normalizePromoCode("   "), "");
});

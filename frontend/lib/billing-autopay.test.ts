/**
 * Unit tests for the autopay-offer gate (lib/billing-autopay.ts).
 *
 * Run with Node's built-in test runner and type stripping:
 *
 *   cd frontend && npm run test:unit
 */

import test from "node:test";
import assert from "node:assert/strict";
import { canOfferAutopay } from "./billing-autopay.ts";

test("supported=true means autopay may be offered", () => {
  assert.equal(canOfferAutopay({ supported: true }), true);
});

test("supported=false means autopay must not be offered", () => {
  assert.equal(canOfferAutopay({ supported: false }), false);
});

test("a missing supported field must not be offered (fail closed)", () => {
  assert.equal(canOfferAutopay({}), false);
});

test("no autopay object at all must not be offered", () => {
  assert.equal(canOfferAutopay(undefined), false);
  assert.equal(canOfferAutopay(null), false);
});

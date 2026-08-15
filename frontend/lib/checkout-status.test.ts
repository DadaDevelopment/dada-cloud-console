import assert from "node:assert/strict";
import test from "node:test";
import type { Payment } from "./api.ts";
import { CHECKOUT_STUCK_AFTER_MS, checkoutReturnState, resumablePaymentUrl } from "./checkout-status.ts";

function payment(over: Partial<Payment>): Payment {
  return {
    id: "p1",
    plan: "startup",
    amount_value: 990,
    currency: "RUB",
    status: "pending",
    created_at: "2026-08-15T10:00:00Z",
    paid_at: null,
    ...over,
  } as Payment;
}

test("a settled payment wins over the clock", () => {
  assert.equal(
    checkoutReturnState(payment({ status: "succeeded" }), CHECKOUT_STUCK_AFTER_MS + 60_000),
    "succeeded",
    "a payment that landed late is a success, not a timeout",
  );
  assert.equal(checkoutReturnState(payment({ status: "canceled" }), 1_000), "canceled");
});

test("a fresh pending payment is still working", () => {
  assert.equal(checkoutReturnState(payment({}), 5_000), "waiting");
});

test("a pending payment past the window is stuck, not silently abandoned", () => {
  assert.equal(checkoutReturnState(payment({}), CHECKOUT_STUCK_AFTER_MS), "stuck");
});

test("a payment we cannot find at all follows the same clock", () => {
  assert.equal(checkoutReturnState(null, 5_000), "waiting");
  assert.equal(checkoutReturnState(undefined, CHECKOUT_STUCK_AFTER_MS + 1), "stuck");
});

test("only a pending payment can be resumed", () => {
  assert.equal(
    resumablePaymentUrl(payment({ confirmation_url: "https://yoomoney.example/c" })),
    "https://yoomoney.example/c",
  );
  assert.equal(
    resumablePaymentUrl(payment({ status: "succeeded", confirmation_url: "https://yoomoney.example/c" })),
    null,
    "offering to pay for a settled payment is how a customer gets charged twice",
  );
  assert.equal(resumablePaymentUrl(payment({ confirmation_url: undefined })), null);
  assert.equal(resumablePaymentUrl(null), null);
});

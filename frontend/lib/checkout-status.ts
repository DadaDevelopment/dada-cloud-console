import type { Payment } from "./api.ts";

/**
 * How long a payment may stay pending after the user comes back from YooKassa
 * before the return screen stops pretending it is still working.
 *
 * A card that is going to settle does so in seconds. Past two minutes the
 * honest answer is "this did not go through", with the means to finish it --
 * not a spinner that runs out of tries and leaves the user staring at a
 * console that never got the plan they just paid for.
 */
export const CHECKOUT_STUCK_AFTER_MS = 120_000;

/**
 * What the return screen should show.
 *
 * "stuck" is the state that did not exist before: the old poller ran twenty
 * tries and then simply stopped, which reads as success to nobody and as
 * failure to everybody.
 */
export type CheckoutReturnState = "waiting" | "succeeded" | "canceled" | "stuck";

/**
 * Decides the return screen's state from the payment row and how long we have
 * been waiting.
 *
 * A terminal row always wins over the clock: a payment that succeeded at
 * second 130 is a success, not a timeout. Only a row that is still pending (or
 * one we have not found at all -- the webhook may not have created it in our
 * view yet) can go stuck.
 */
export function checkoutReturnState(
  payment: Pick<Payment, "status"> | null | undefined,
  elapsedMs: number,
): CheckoutReturnState {
  if (payment?.status === "succeeded") return "succeeded";
  if (payment?.status === "canceled") return "canceled";
  return elapsedMs >= CHECKOUT_STUCK_AFTER_MS ? "stuck" : "waiting";
}

/**
 * The URL that finishes an unfinished payment, or null when there is nothing
 * to resume.
 *
 * YooKassa keeps a confirmation page valid for the life of its payment, so the
 * cheapest recovery for someone who closed the tab is to send them back to the
 * same page -- not to charge a second payment they will then have to get
 * refunded. Terminal rows never carry the URL (the backend strips it), so this
 * cannot offer to pay twice.
 */
export function resumablePaymentUrl(payment: Payment | null | undefined): string | null {
  if (!payment || payment.status !== "pending") return null;
  return payment.confirmation_url || null;
}

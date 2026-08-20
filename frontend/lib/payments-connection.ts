import type { PaymentsConnection, PaymentsWebhook } from "@/lib/api";

/**
 * Returns the webhook list of a payments connection, or an empty list when
 * the field is absent.
 *
 * PaymentsConnection.webhooks is typed nullable: 8 error-boundary crashes
 * from 4 users were traced to `.map` running directly on this field on
 * frontend/components/payments/payments-manager.tsx's settings tab (backlog
 * 0402). The concrete source of a null value was never proven -- the backend
 * handler (payments_connect.go PaymentsStatus) always fills it today -- but
 * the crash was real, so the render path no longer trusts the field to be
 * present.
 *
 * @param connection - the connection object, or the relevant slice of it
 */
export function paymentsWebhooks(connection: Pick<PaymentsConnection, "webhooks"> | null | undefined): PaymentsWebhook[] {
  return connection?.webhooks ?? [];
}

/**
 * Returns the injected env-var key names of a payments connection, or an
 * empty list when the field is absent. See {@link paymentsWebhooks} for why
 * this defends against null.
 *
 * @param connection - the connection object, or the relevant slice of it
 */
export function paymentsEnvKeys(connection: Pick<PaymentsConnection, "env_keys"> | null | undefined): string[] {
  return connection?.env_keys ?? [];
}

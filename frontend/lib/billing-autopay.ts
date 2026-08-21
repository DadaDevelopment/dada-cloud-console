/**
 * Whether the console may offer automatic renewal at all.
 *
 * Grounded in live audit_events, 2026-08-15 21:45:43 UTC: this merchant
 * account cannot make recurring charges -- YooKassa answers checkout with
 * 403 "This store can't make recurring payments" (error_class
 * yk_forbidden). The one stranger who ever reached checkout ticked the old
 * always-present consent box and left with nothing. The backend now reports
 * `autopay.supported` on GET /projects/{id}/billing/account and flips it to
 * true only once the merchant capability is actually turned on.
 *
 * A MISSING field (backend not yet deployed, or an older account snapshot)
 * reads as unsupported by design -- fail closed, never offer a doomed
 * option in that window.
 */
export interface AutopaySupportInput {
  supported?: boolean;
}

export function canOfferAutopay(autopay: AutopaySupportInput | null | undefined): boolean {
  return autopay?.supported === true;
}

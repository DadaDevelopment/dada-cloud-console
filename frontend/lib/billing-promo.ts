/**
 * Maps a promo-redeem failure's machine-readable `code` (from
 * POST /api/v1/billing/promo/redeem, see backend/internal/api/billing_promo.go)
 * to the i18n message key that explains it.
 *
 * This repo has a standing rule against classifying API errors by parsing
 * response prose on the frontend -- every branch here keys off `err.code`
 * only, never `err.message`. An unrecognized or missing code (a future
 * backend failure mode this client has not been taught yet, or a network
 * error with no code at all) falls back to a generic message rather than
 * throwing or showing nothing.
 */
const KNOWN_PROMO_ERROR_CODES = [
  "promo_code_required",
  "promo_code_not_found",
  "promo_code_expired",
  "promo_code_exhausted",
  "promo_already_redeemed",
  "promo_org_unresolved",
] as const;

export type PromoErrorCode = (typeof KNOWN_PROMO_ERROR_CODES)[number];

export function promoErrorMessageKey(code: string | null | undefined): string {
  if (code && (KNOWN_PROMO_ERROR_CODES as readonly string[]).includes(code)) {
    return `billing.promo.error.${code}`;
  }
  return "billing.promo.error.generic";
}

/**
 * A code the redeem button may submit: trimmed and upper-cased the same way
 * the backend normalizes it before lookup, so a client-side "code required"
 * check (empty after trimming) matches the backend's own rule exactly.
 */
export function normalizePromoCode(input: string): string {
  return input.trim().toUpperCase();
}

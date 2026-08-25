/**
 * The grace copy says the account already exceeds Free, so a deadline alone
 * is not enough to show it. Every Free account was grandfathered during the
 * rollout, including accounts whose current footprint still fits the plan.
 */
export function shouldShowQuotaGraceWarning(
  graceDate: string | null,
  overLimit: readonly unknown[],
): boolean {
  return graceDate !== null && overLimit.length > 0;
}

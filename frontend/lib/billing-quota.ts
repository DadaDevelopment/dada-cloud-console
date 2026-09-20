/**
 * The grace copy says the account already exceeds Free, so a deadline alone
 * is not enough to show it. Every Free account was grandfathered during the
 * rollout, including accounts whose current footprint still fits the plan.
 */
export function shouldShowQuotaGraceWarning(
  graceDate: string | null,
  overLimit: readonly unknown[],
): graceDate is string {
  return graceDate !== null && overLimit.length > 0;
}

/** One resource measured against the plan ceiling that will start refusing it. */
export interface QuotaRow {
  used: number;
  limit: number;
}

/**
 * How close an account is to the wall, per resource.
 *
 * `over` is already above the ceiling and loses the ability to create more the
 * moment grandfathering ends. `at` sits exactly on it: nothing is lost today,
 * but the next create is the one that gets refused, and that user currently
 * gets no warning at all before the 403.
 */
export interface QuotaPressure {
  over: string[];
  at: string[];
}

/**
 * Splits quota rows into the ones already past the ceiling and the ones sitting
 * on it.
 *
 * A limit of 0 means "unlimited on this plan" in the wire format, so it is
 * never pressure. Rows the API omitted are skipped rather than assumed empty:
 * inventing pressure from missing data would warn a user about a wall we have
 * not actually measured.
 */
export function quotaPressure(
  quotas: Partial<Record<string, QuotaRow | undefined | null>> | null | undefined,
  order: readonly string[],
): QuotaPressure {
  const over: string[] = [];
  const at: string[] = [];
  for (const key of order) {
    const row = quotas?.[key];
    if (!row || row.limit <= 0) continue;
    if (row.used > row.limit) over.push(key);
    else if (row.used === row.limit) at.push(key);
  }
  return { over, at };
}

/**
 * Where a console user who needs a bigger plan must actually be sent.
 *
 * Every quota surface used to link to the marketing `/pricing` page, whose plan
 * buttons point at `/login`. A logged-in user following that link is bounced
 * straight back to `/projects` by the login route, so the upgrade intent dies
 * in the hop and there is no way to reach checkout at all: the paid plans exist
 * only behind the in-console billing page. This returns that page, anchored at
 * the plan list, so the CTA lands on the buy button rather than on marketing.
 *
 * Returns null when no project is known. Callers MUST hide the CTA in that case
 * instead of building a link anyway -- a CTA to `/projects/undefined/billing`
 * is exactly the dead link a live user clicked four times in September.
 */
export function quotaUpgradeHref(projectId: string | null | undefined): string | null {
  if (!projectId) return null;
  return `/projects/${projectId}/billing#billing-plans`;
}

import type { BillingPlan, BillingQuota } from "./api.ts";

/** Resources the quota gate can refuse, mapped to the plan quota that raises them. */
export const QUOTA_FIELD: Record<string, keyof BillingQuota> = {
  apps: "apps",
  databases: "databases",
  storage_gb: "storage_gb",
  domains: "domains",
  team_members: "team_members",
  app_servers: "app_servers",
};

/** Binary multipliers, in GB, for the Kubernetes quantity suffixes we ship. */
const SIZE_UNITS: Record<string, number> = {
  Ki: 1 / (1024 * 1024),
  Mi: 1 / 1024,
  Gi: 1,
  Ti: 1024,
  K: 1e-6,
  M: 1e-3,
  G: 1,
  T: 1000,
};

/**
 * Reads a catalog volume size ("100Gi", "512Mi", "1Ti") as whole GB, rounded
 * up.
 *
 * Rounding up is deliberate: a 1.5 GB volume needs a plan that allows 2, and
 * offering a plan that still cannot hold the thing is worse than offering
 * nothing. Returns null for anything unparseable, which every caller must read
 * as "unknown, do not claim it is blocked" -- a badge invented from a size we
 * failed to parse would grey out a tile the user could actually install.
 */
export function parseVolumeSizeGB(size: string | null | undefined): number | null {
  if (!size) return null;
  const match = /^([0-9]+(?:\.[0-9]+)?)\s*([A-Za-z]*)$/.exec(size.trim());
  if (!match) return null;
  const value = Number(match[1]);
  if (!Number.isFinite(value)) return null;
  const suffix = match[2];
  if (!suffix) return Math.ceil(value / (1024 * 1024 * 1024));
  const unit = SIZE_UNITS[suffix];
  if (unit == null) return null;
  return Math.ceil(value * unit);
}

/**
 * The cheapest paid plan that actually clears a requirement on one quota.
 *
 * `required` is what the user is trying to do (a 100 GB volume), `currentLimit`
 * is what the plan they hold allows. Comparing against `required` is the whole
 * point: picking a plan merely larger than the current limit is how a 100 GB
 * install gets offered a 50 GB plan and the user pays to hit the same wall.
 * With no `required` known it degrades to "anything strictly larger than the
 * current limit", which is the old upsell behaviour.
 *
 * Returns null when nothing qualifies -- already on the largest plan, or the
 * catalog could not be read. Callers must then fall back to the plain pricing
 * link rather than showing a checkout for a plan that does not exist.
 */
export function pickTargetPlan(
  plans: BillingPlan[] | null | undefined,
  resource: string,
  opts: { currentLimit?: number | null; required?: number | null } = {},
): BillingPlan | null {
  const field = QUOTA_FIELD[resource];
  if (!plans || !field) return null;
  const { currentLimit = null, required = null } = opts;

  const candidates = plans
    .filter((p) => (p.price_rub ?? 0) > 0)
    .filter((p) => {
      const quota = p.quotas?.[field];
      if (quota == null) return false;
      if (required != null) return quota >= required;
      return currentLimit == null || quota > currentLimit;
    })
    .sort((a, b) => (a.price_rub ?? 0) - (b.price_rub ?? 0));

  return candidates[0] ?? null;
}

/** What a catalog tile costs the user, given the plan they hold today. */
export interface SolutionReach {
  /** False only when we are certain the current plan cannot hold this tile. */
  reachable: boolean;
  /** GB the tile's volume needs, or null when it has none or it is unparseable. */
  requiredGB: number | null;
  /** The cheapest plan that can hold it. Null when reachable, or when no plan can. */
  plan: BillingPlan | null;
}

/**
 * Decides whether a catalog tile is installable on the current plan, and which
 * plan it belongs to when it is not.
 *
 * Twenty of twenty-seven tiles ask for more storage than the free plan allows,
 * and the only way a user learned that was to fill in a form, submit, and get a
 * 403 back. Computing it here lets the catalog say so up front, on the tile,
 * before any of that work is spent.
 *
 * This is a display hint, never an authorisation: the backend quota gate stays
 * the source of truth, and everything unknown here (missing catalog, missing
 * limit, unparseable size) resolves to `reachable: true` so a gap in our data
 * can never lock a user out of something they are entitled to.
 */
export function solutionReach(
  volumeSize: string | null | undefined,
  storageLimitGB: number | null | undefined,
  plans: BillingPlan[] | null | undefined,
): SolutionReach {
  const requiredGB = parseVolumeSizeGB(volumeSize);
  if (requiredGB == null || storageLimitGB == null || requiredGB <= storageLimitGB) {
    return { reachable: true, requiredGB, plan: null };
  }
  return {
    reachable: false,
    requiredGB,
    plan: pickTargetPlan(plans, "storage_gb", { currentLimit: storageLimitGB, required: requiredGB }),
  };
}

/**
 * Tailwind classes for a plan badge, keyed by plan.
 *
 * Locked tiles are sold, not greyed out: a tile the user cannot install today
 * is still the tile they came for, and colouring it like an error teaches them
 * the catalog is broken rather than that a plan unlocks it. Each paid plan gets
 * its own colour so the badge reads as a price tier.
 */
export function planBadgeClasses(planKey: string): string {
  switch (planKey) {
    case "startup":
      return "bg-blue-100 text-blue-700 dark:bg-blue-950/60 dark:text-blue-300";
    case "business":
      return "bg-violet-100 text-violet-700 dark:bg-violet-950/60 dark:text-violet-300";
    case "enterprise":
      return "bg-amber-100 text-amber-800 dark:bg-amber-950/60 dark:text-amber-300";
    default:
      return "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300";
  }
}

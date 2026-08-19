import type { AdminOverviewPlatformHealth } from "@/lib/types";

/** How long a snapshot stays trustworthy before its silence starts to look like health. */
export const PLATFORM_HEALTH_STALE_AFTER_SECONDS = 5 * 60;

export type PlatformHealthState = "blind" | "healthy" | "unhealthy";

/**
 * Which of the three states the platform-health card should render.
 *
 * observed=false always wins as "blind" regardless of what unhealthy holds --
 * a check that never ran cannot vouch for an empty list.
 */
export function platformHealthState(
  health: Pick<AdminOverviewPlatformHealth, "observed" | "unhealthy">
): PlatformHealthState {
  if (!health.observed) return "blind";
  return health.unhealthy.length > 0 ? "unhealthy" : "healthy";
}

/**
 * Seconds elapsed since checked_at. Returns Infinity for an unparsable
 * timestamp so callers treat it as maximally stale rather than as fresh.
 */
export function platformHealthAgeSeconds(checkedAt: string, nowMs: number = Date.now()): number {
  const checked = new Date(checkedAt).getTime();
  if (Number.isNaN(checked)) return Number.POSITIVE_INFINITY;
  return Math.max(0, Math.floor((nowMs - checked) / 1000));
}

/** Whether the snapshot is old enough that it should no longer be trusted as current. */
export function platformHealthIsStale(checkedAt: string, nowMs: number = Date.now()): boolean {
  return platformHealthAgeSeconds(checkedAt, nowMs) > PLATFORM_HEALTH_STALE_AFTER_SECONDS;
}

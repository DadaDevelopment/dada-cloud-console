import type { AdminOverviewLiveUrls } from "@/lib/types";

/**
 * Share of checked apps that answered healthy, as a rounded percentage.
 * Returns null when nothing was checked -- there is no ratio to show.
 */
export function liveUrlOkShare(live: Pick<AdminOverviewLiveUrls, "checked" | "ok">): number | null {
  if (live.checked <= 0) return null;
  return Math.round((live.ok / live.checked) * 100);
}

/**
 * Human title for the last-mile card, e.g. "30 из 42 (71%)".
 * Falls back to a neutral label when there is nothing checked yet.
 */
export function liveUrlHeadline(live: Pick<AdminOverviewLiveUrls, "checked" | "ok">): string {
  const share = liveUrlOkShare(live);
  if (share === null) return "нечего проверять";
  return `${live.ok} из ${live.checked} (${share}%)`;
}

/**
 * Whether the stale count is large enough relative to what was checked that
 * an empty dead_apps list must not be read as "everything is fine" -- it may
 * just mean the probe did not run.
 */
export function liveUrlStaleDominates(live: Pick<AdminOverviewLiveUrls, "checked" | "stale">): boolean {
  if (live.stale <= 0) return false;
  if (live.checked <= 0) return true;
  return live.stale >= live.checked * 0.2;
}

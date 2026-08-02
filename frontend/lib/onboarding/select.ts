import type { OnboardingCampaign } from "./types";

export interface SelectContext {
  pathname: string;
  hasTarget: (selector: string) => boolean;
}

/** Default wait before a campaign is allowed to fire, when it declares none. */
export const DEFAULT_DELAY_MS = 3000;

/**
 * How long a lower-priority campaign waits for a higher-priority anchor to
 * mount. Page-level anchors appear only after the page's data loads, while
 * shell-level anchors (the agent FAB) exist from the first paint — without this
 * grace the shell campaign always wins the race.
 *
 * Only campaigns whose route matches the current page count as higher priority:
 * a campaign that belongs to another page is never going to mount here, so
 * making anything wait on it would be waiting on nothing.
 */
export const LOWER_PRIORITY_GRACE_MS = 4000;

/** After this the anchor is treated as never coming and polling stops. */
export const SELECT_WINDOW_MS = 10000;

/**
 * Returns the campaign that should start right now, or null to keep waiting.
 *
 * @param elapsedMs time since the status map resolved, i.e. since the page
 * became eligible for a tour.
 */
export function selectCampaignToFire(
  campaigns: OnboardingCampaign[],
  statusMap: Record<string, string>,
  ctx: SelectContext,
  elapsedMs: number,
): OnboardingCampaign | null {
  if (elapsedMs > SELECT_WINDOW_MS) return null;
  const pending = campaigns.filter(
    (c) => !statusMap[c.key] && (!c.route || c.route(ctx.pathname)),
  );
  const campaign = selectPendingCampaign(pending, statusMap, ctx);
  if (!campaign) return null;
  if (elapsedMs < (campaign.delayMs ?? DEFAULT_DELAY_MS)) return null;
  if (campaign !== pending[0] && elapsedMs < LOWER_PRIORITY_GRACE_MS) return null;
  return campaign;
}

export function selectPendingCampaign(
  campaigns: OnboardingCampaign[],
  statusMap: Record<string, string>,
  ctx: SelectContext,
): OnboardingCampaign | null {
  for (const campaign of campaigns) {
    if (statusMap[campaign.key]) continue;
    if (campaign.route && !campaign.route(ctx.pathname)) continue;
    const first = campaign.steps[0];
    if (!first || !ctx.hasTarget(first.target)) continue;
    return campaign;
  }
  return null;
}

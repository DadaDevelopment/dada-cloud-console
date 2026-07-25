import type { OnboardingCampaign } from "./types";

export interface SelectContext {
  pathname: string;
  hasTarget: (selector: string) => boolean;
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

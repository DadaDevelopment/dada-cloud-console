import type { OnboardingCampaign } from "./types";

export const ONBOARDING_CAMPAIGNS: OnboardingCampaign[] = [
  {
    key: "agent",
    docsUrl: "/developer/mcp-ai-agents",
    delayMs: 3000,
    steps: [
      {
        target: '[data-onboarding="agent-fab"]',
        titleKey: "onboarding.agent.title",
        bodyKey: "onboarding.agent.body",
      },
    ],
  },
];

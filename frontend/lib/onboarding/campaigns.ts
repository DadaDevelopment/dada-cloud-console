import type { OnboardingCampaign } from "./types";

export const ONBOARDING_CAMPAIGNS: OnboardingCampaign[] = [
  {
    key: "first-deploy",
    docsUrl: "/developer/applications-deploy-from-github",
    delayMs: 1500,
    steps: [
      {
        target: '[data-onboarding="first-deploy"]',
        titleKey: "onboarding.firstDeploy.title",
        bodyKey: "onboarding.firstDeploy.body",
      },
    ],
  },
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

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
    key: "ai-routing",
    docsUrl: "/developer/llm-providers",
    delayMs: 1500,
    route: (pathname) => pathname.endsWith("/ai"),
    steps: [
      {
        target: '[data-onboarding="ai-routing"]',
        titleKey: "onboarding.aiRouting.title",
        bodyKey: "onboarding.aiRouting.body",
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

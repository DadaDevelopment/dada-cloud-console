export type OnboardingStatus = "seen" | "skipped" | "done";

export interface OnboardingStep {
  target: string;
  titleKey: string;
  bodyKey: string;
}

export interface OnboardingCampaign {
  key: string;
  steps: OnboardingStep[];
  docsUrl: string;
  delayMs?: number;
  route?: (pathname: string) => boolean;
}

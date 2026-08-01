/**
 * Ready-app-no-next-step: an app that reached `Ready` with no alerts renders
 * a wall of green status cards and nothing to act on. Live ux_events show a
 * returning user opening a Ready app for about a second and leaving without
 * a single mutating action - the page has nothing to point them at.
 *
 * This computes which "next step" suggestions apply, from data the app
 * detail page already has loaded (no extra backend call). Order is the
 * display priority; the card caller slices to a max of three.
 */

export type NextStepId = "connect_domain" | "connect_git" | "deploy_commit";

export interface NextStepInput {
  hasCustomDomain: boolean;
  hasGitRepo: boolean;
}

const MAX_STEPS = 3;

/**
 * `hasCustomDomain` should be false both when no hostname exists yet and
 * when only the managed surrogate (`<app>-<hash>.dada-tuda.ru`) is present -
 * a surrogate domain is not something the user chose.
 */
export function getAppNextSteps({ hasCustomDomain, hasGitRepo }: NextStepInput): NextStepId[] {
  const steps: NextStepId[] = [];
  if (!hasCustomDomain) steps.push("connect_domain");
  if (hasGitRepo) {
    steps.push("deploy_commit");
  } else {
    steps.push("connect_git");
  }
  return steps.slice(0, MAX_STEPS);
}

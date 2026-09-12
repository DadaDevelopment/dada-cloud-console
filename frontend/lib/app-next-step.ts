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

export type NextStepId = "publish_web" | "connect_domain" | "connect_git" | "deploy_commit";

export interface NextStepInput {
  hasCustomDomain: boolean;
  hasGitRepo: boolean;
  isWorker?: boolean;
}

const MAX_STEPS = 3;

/**
 * `hasCustomDomain` should be false both when no hostname exists yet and
 * when only the managed surrogate (`<app>-<hash>.dada-tuda.ru`) is present -
 * a surrogate domain is not something the user chose.
 *
 * `isWorker` changes which domain step is honest. An upload whose detection
 * found no web framework and no EXPOSE is filed as a worker
 * (isWorkerUpload in backend/internal/api/uploadsource.go), and a worker is
 * deliberately never minted a hostname: appNeedsDefaultDomain in
 * backend/internal/api/domains.go returns false for it, so no address is
 * issued and no backfill pass will ever issue one. Offering "connect your
 * own domain" there points at a dead end - a custom hostname in front of a
 * workload the platform believes serves no HTTP. The live cost of staying
 * silent is measured: yzfy@sls.xx.kg uploaded twice on 2026-09-05/09-07,
 * both archives were filed worker (port 0), neither app ever got a URL, and
 * the session ended with the source downloaded back and the app deleted.
 * The lever that fixes it exists and is one field away - saving a port via
 * UpdateAppPort clears the worker flag and lets the domain be minted - so
 * the step points there instead.
 */
export function getAppNextSteps({ hasCustomDomain, hasGitRepo, isWorker = false }: NextStepInput): NextStepId[] {
  const steps: NextStepId[] = [];
  if (isWorker) {
    steps.push("publish_web");
  } else if (!hasCustomDomain) {
    steps.push("connect_domain");
  }
  if (hasGitRepo) {
    steps.push("deploy_commit");
  } else {
    steps.push("connect_git");
  }
  return steps.slice(0, MAX_STEPS);
}

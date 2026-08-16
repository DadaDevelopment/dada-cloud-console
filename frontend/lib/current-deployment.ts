import type { Deployment } from "@/lib/types";

export interface CurrentDeploymentState {
  kind: "current" | "pending";
  deployment: Deployment;
}

/**
 * How long a freshly recorded deployment is read as "still landing" rather
 * than silently dropped. `ListDeployments` derives `is_current` from the
 * app's actual running image (resource_snapshots), which only updates once
 * the gitops reconciler observes the new pod -- so for a window after a
 * deploy op is created, no row carries `is_current: true` yet even though
 * the platform already sent the image. A live user read that silent window
 * as "nothing happened" and manually re-deployed the same digest 85 seconds
 * after the platform's own automatic deploy.
 */
const PENDING_WINDOW_MS = 15 * 60 * 1000;

/**
 * Picks the one deployment worth showing as "this is what's live" on the app
 * page. `deployments` must be ordered most-recent-first (the API's own
 * ORDER BY created_at DESC).
 *
 * - A deployment with `is_current: true` wins outright: it is the row whose
 *   image matches what is actually running.
 * - Otherwise, if nothing is marked current yet, the newest deployment is
 *   read as "pending" as long as it was recorded within `PENDING_WINDOW_MS`
 *   -- honest about not knowing whether it landed, but distinct from silence.
 * - Past that window, or with no deployments at all, returns null: absence
 *   of data must never render as a verdict either way.
 */
export function selectCurrentDeployment(
  deployments: Deployment[] | null | undefined,
  nowMs: number = Date.now()
): CurrentDeploymentState | null {
  if (!deployments || deployments.length === 0) return null;
  const current = deployments.find((d) => d.is_current);
  if (current) return { kind: "current", deployment: current };
  const newest = deployments[0];
  const age = nowMs - new Date(newest.created_at).getTime();
  if (age >= 0 && age <= PENDING_WINDOW_MS) return { kind: "pending", deployment: newest };
  return null;
}

/**
 * True when `deploymentId` is the most recently recorded deployment.
 * `deployments` must be ordered most-recent-first, same contract as
 * `selectCurrentDeployment`. Used to tell the newest, not-yet-confirmed row
 * apart from a genuinely older one in the deploy feed: rolling "back" to the
 * newest thing ever deployed is not a rollback, it is a redeploy.
 */
export function isNewestDeployment(deploymentId: string, deployments: Deployment[] | null | undefined): boolean {
  if (!deployments || deployments.length === 0) return false;
  return deployments[0].id === deploymentId;
}

/**
 * True when `deploymentId` is the newest deployment AND nothing is confirmed
 * current yet AND that newest row is still inside `PENDING_WINDOW_MS` -- i.e.
 * the platform is plausibly still rolling it out right now.
 *
 * The window matters: a deploy that never landed (image pull failure, a pod
 * that never became ready) stays newest-and-not-current forever, and a badge
 * without an age bound would spin "Deploying" on it indefinitely. Past the
 * window the row simply carries no badge -- not knowing is honest, claiming
 * an in-flight rollout that ended long ago is not.
 */
export function isPendingDeployment(
  deploymentId: string,
  deployments: Deployment[] | null | undefined,
  nowMs: number = Date.now()
): boolean {
  const state = selectCurrentDeployment(deployments, nowMs);
  return state?.kind === "pending" && state.deployment.id === deploymentId;
}

/**
 * Shortens a digest-pinned image reference (`repo@sha256:<64 hex>`) to a
 * readable form (`repo@sha256:<12 hex>`), matching the short-sha convention
 * used elsewhere in the console (build-commit.ts). Tag-pinned references
 * (`repo:tag`) are already short and pass through unchanged.
 */
export function shortImageRef(imageUri: string): string {
  const marker = "@sha256:";
  const at = imageUri.indexOf(marker);
  if (at === -1) return imageUri;
  const repo = imageUri.slice(0, at);
  const digest = imageUri.slice(at + marker.length);
  return `${repo}${marker}${digest.slice(0, 12)}`;
}

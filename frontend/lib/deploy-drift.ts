import type { Deployment } from "@/lib/types";

export interface DeployDriftVerdict {
  currentDeploymentId: string;
  latestDeploymentId: string;
}

/**
 * The drift signal for one app's deployment history: the pod is running a
 * different deployment than the one the console last recorded. Detected the
 * same way the backend derives `is_current` in `ListDeployments`
 * (backend/internal/api/deployments.go) -- by comparing each deployment's
 * image_uri against the app's actual running image from resource_snapshots
 * -- so this component names the same fact the deployments list already
 * shows, just surfaced earlier on the app page.
 *
 * `deployments` must be ordered most-recent-first, matching the API's
 * ORDER BY created_at DESC (deploymentsApi.list already returns it that
 * way). Drift exists when the newest deployment is NOT the one currently
 * running (some older row carries `is_current: true` instead) -- the
 * megafactory shape: a later deploy landed a ledger row but never actually
 * reached the pod.
 *
 * Returns null whenever there is nothing honest to say: fewer than two
 * deployments, the newest one is already current, or -- when the running
 * image is unknown (no snapshot yet) -- no deployment is marked current at
 * all. Absence of data must never render as a drift verdict either way.
 */
export function evaluateDeployDrift(
  deployments: Deployment[] | null | undefined
): DeployDriftVerdict | null {
  if (!deployments || deployments.length < 2) return null;
  const latest = deployments[0];
  if (latest.is_current) return null;
  const current = deployments.find((d) => d.is_current);
  if (!current) return null;
  return { currentDeploymentId: current.id, latestDeploymentId: latest.id };
}

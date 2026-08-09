/**
 * Phases at which a resource has stopped moving on its own. A resource in any
 * other phase (Pending, Provisioning, Creating, Unknown, ...) is still settling,
 * so a list page polls until it reaches one of these. NotDeployed/Stopped are
 * terminal on purpose: a connected-but-undeployed repo or a stopped app is a
 * stable state, not an in-flight one, and must not drive an endless poll.
 * Unreachable is terminal for the same reason: the app is running and settled,
 * it just does not answer HTTP, and only a redeploy changes that — polling would
 * never see it move.
 */
export const TERMINAL_PHASES = new Set([
  "ready",
  "failed",
  "notdeployed",
  "stopped",
  "orphaned",
  "unreachable",
]);

/**
 * isSettling reports whether any resource in the list is still transitioning
 * (optimistic Pending row waiting for the reconciler, a provisioning workload,
 * ...), i.e. whether the list is worth re-fetching to catch its next phase.
 */
export function isSettling(items: Array<{ phase?: string }>): boolean {
  return items.some((x) => !TERMINAL_PHASES.has((x.phase ?? "").toLowerCase()));
}

/**
 * Show condition and persisted dismissal for the "your app is live" banner
 * on the app detail page.
 *
 * Measured leak: the only "you're live" signal in the console today is the
 * build-finish panel in `BuildWatcher` (lib/build-watch.ts), which only ever
 * renders while the tab that started the build is still open. Anyone who
 * leaves before the build resolves -- the common case, per that file's own
 * numbers -- never learns their app has a working public address, even
 * though `apps.url.reason.awaiting_first_deploy` already promises them one.
 *
 * This module makes liveness a derived fact of the persisted resource
 * snapshot (phase + url_status + url_reason), not of a build the browser
 * happened to watch, so the confirmation survives navigation and reload.
 * Dismissal is a flat localStorage flag per project+app: there is no
 * server-side "first successful deploy" marker to hang it on, and a flag
 * that never resets is simpler and good enough -- once a person has seen
 * their own live URL once, showing it again after a reload is not the
 * failure mode this exists to fix.
 */

const DISMISS_KEY_PREFIX = "dada_live_banner_dismissed:";

function dismissKey(projectId: string, appName: string): string {
  return `${DISMISS_KEY_PREFIX}${projectId}:${appName}`;
}

/** True whenever the flag cannot be read at all (no window, storage blocked), so the banner fails closed rather than nagging. */
export function isLiveBannerDismissed(projectId: string, appName: string): boolean {
  if (typeof window === "undefined") return true;
  try {
    return window.localStorage.getItem(dismissKey(projectId, appName)) === "1";
  } catch {
    return true;
  }
}

const listeners = new Set<() => void>();

/**
 * Subscription seam for `useSyncExternalStore`: localStorage is an external
 * store, so the banner reads it through the React-sanctioned path instead of
 * mirroring it into state from an effect.
 */
export function subscribeLiveBannerDismissal(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/** Best-effort write. A blocked or full storage just means the banner keeps reappearing, which is safe. */
export function dismissLiveBanner(projectId: string, appName: string): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(dismissKey(projectId, appName), "1");
  } catch {
    return;
  } finally {
    listeners.forEach((listener) => listener());
  }
}

export interface AppLiveBannerParams {
  projectId: string;
  appName: string;
  url: string | null | undefined;
  phase: string | null | undefined;
  urlStatus: string | null | undefined;
  urlReason: string | null | undefined;
}

/**
 * Whether the app is, right now, reachable at a real address -- the
 * workload is Ready/Running AND the route itself resolved, not merely
 * assigned. `url_reason === "awaiting_first_deploy"` is checked explicitly
 * even though `urlStatus !== "active"` already implies it, because that is
 * the exact reason string the console already promises the user in
 * `apps.url.reason.awaiting_first_deploy` and the two must never drift apart.
 */
export function isAppLive(params: Pick<AppLiveBannerParams, "url" | "phase" | "urlStatus" | "urlReason">): boolean {
  if (!params.url) return false;
  const phase = (params.phase ?? "").toLowerCase();
  if (phase !== "ready" && phase !== "running") return false;
  if (params.urlStatus !== "active") return false;
  if (params.urlReason === "awaiting_first_deploy") return false;
  return true;
}

export function shouldShowLiveBanner(params: AppLiveBannerParams): boolean {
  if (!isAppLive(params)) return false;
  return !isLiveBannerDismissed(params.projectId, params.appName);
}

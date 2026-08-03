/**
 * Persists builds the current browser triggered so `BuildWatcher` can report
 * on them after the user has navigated away from the build page.
 *
 * Measured leak (2026-08, live audit_events + ux_events): TriggerBuild is the
 * terminal action for 3 of 8 users who ever did anything in the console — 8
 * manual builds succeeded, but only 1 view and 0 clicks landed on the
 * build-success panel, because that panel only renders while the user is
 * still sitting on the build page when the build resolves. Most people
 * navigate off first and never learn their app went live (or broke).
 *
 * Storage is a flat list capped at `MAX_TRACKED` entries and pruned of
 * anything older than `MAX_AGE_MS`, so a browser that is never revisited
 * cannot grow this list forever and a build that finished days ago cannot
 * resurface as "new".
 */

export interface TrackedBuild {
  projectId: string;
  envId: string;
  appName: string;
  buildId: string;
  startedAt: number;
}

const STORAGE_KEY = "dada_tracked_builds";
const MAX_TRACKED = 5;
const MAX_AGE_MS = 2 * 60 * 60 * 1000;

function isTrackedBuild(value: unknown): value is TrackedBuild {
  if (!value || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  return (
    typeof v.projectId === "string" &&
    typeof v.envId === "string" &&
    typeof v.appName === "string" &&
    typeof v.buildId === "string" &&
    typeof v.startedAt === "number"
  );
}

function fresh(entries: TrackedBuild[]): TrackedBuild[] {
  const cutoff = Date.now() - MAX_AGE_MS;
  return entries.filter((e) => e.startedAt >= cutoff);
}

/** Reads the tracked-build list, dropping anything malformed or expired. Never throws. */
export function readTrackedBuilds(): TrackedBuild[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return fresh(parsed.filter(isTrackedBuild));
  } catch {
    return [];
  }
}

/** Best-effort write: a private-mode or full storage failure is swallowed, tracking just stops. */
function writeTrackedBuilds(entries: TrackedBuild[]): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(entries));
  } catch {
    return;
  }
}

/**
 * Records a build the user just triggered so `BuildWatcher` can pick it up
 * even if this tab reloads or the user leaves the build page. Call this
 * right after a successful `buildsApi.trigger(...)`.
 */
export function trackBuildStart(entry: Omit<TrackedBuild, "startedAt">): void {
  const existing = readTrackedBuilds().filter((e) => e.buildId !== entry.buildId);
  const next = [...existing, { ...entry, startedAt: Date.now() }].slice(-MAX_TRACKED);
  writeTrackedBuilds(next);
}

/** Removes one build from the tracked list, e.g. once its terminal status has been observed. */
export function untrackBuild(buildId: string): void {
  writeTrackedBuilds(readTrackedBuilds().filter((e) => e.buildId !== buildId));
}

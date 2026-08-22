/**
 * The last-mile verdict for one app: what a real visitor gets when they
 * open the app's public address right now, as measured by an in-cluster
 * HTTP probe (see gitops-agent/internal/worker/livenessprobe.go), not
 * inferred from pod/build health. A green build and a Ready pod say
 * nothing about whether the ingress in front of them actually answers --
 * this is the field that closes that gap.
 *
 * The backend writes `http_status` / `http_reason` / `http_checked_at`
 * onto `resource_snapshots.summary_json` for kind='App' and the app-list
 * endpoint passes summary_json through unchanged, so these three keys
 * reach the console exactly as gitops-agent wrote them.
 */
export interface LastMileSummary {
  http_status?: number;
  http_reason?: string;
  http_checked_at?: string;
  worker?: boolean;
}

export interface LastMileVerdict {
  status: number;
  reason: string;
  checkedAt: string;
}

/**
 * Same "dead" boundary the admin `live_urls` panel uses
 * (isDeadProbeStatus, backend/internal/api/admin_overview.go): `http_status
 * === 0` (probe attempted, target never answered -- dial error, timeout, or
 * the probe itself failed to build), or one of `502`/`503`/`504` -- the
 * codes an ingress controller itself generates when there is no backend pod
 * behind the route at all. A 2xx/3xx is alive.
 *
 * Any other non-2xx/3xx status (404, 401, 500, ...) means the application
 * itself answered -- proof the last mile is NOT dead, whatever it chose to
 * say. Two real apps (a headless bot and a tracker service) had no route at
 * "/" and answered 404 there while their own /health route answered 200 the
 * whole time; folding that into "dead" made a live app indistinguishable
 * from a stale ingress pointing at a deleted backend. isDeadHTTPStatus is
 * the single predicate both branches below key off of.
 */
export function isDeadHTTPStatus(status: number): boolean {
  return status === 0 || status === 502 || status === 503 || status === 504;
}

/**
 * Prefix gitops-agent writes into http_reason when a 502/503/504 arrived
 * with a body the application itself produced instead of ingress-nginx's
 * default error page (classifyLivenessResponse,
 * gitops-agent/internal/worker/livenessprobe.go).
 */
const APP_AUTHORED_REASON_PREFIX = "app_status_";

/**
 * The full dead predicate, twin of isDeadProbeResult in
 * backend/internal/api/admin_overview.go: a 5xx only proves the route has no
 * backend when nothing behind the route authored it. Measured on production
 * 2026-08-15, fonbet-value answered 503 from its own container (JSON body
 * listing its readiness blockers) while ingress-nginx served the same status
 * class for n8n, whose Service no longer exists -- so the status is read
 * together with the emitter the probe recorded. `http_status === 0` stays
 * dead unconditionally: no answer at all carries no authorship, whatever the
 * reason string happens to say.
 */
export function isDeadLastMile(status: number, reason: string): boolean {
  if (!isDeadHTTPStatus(status)) return false;
  if (status === 0) return true;
  return !reason.startsWith(APP_AUTHORED_REASON_PREFIX);
}

/**
 * Returns null whenever there is nothing honest to say: no probe has run
 * yet (`http_checked_at` absent), the probe found the address serving
 * (2xx/3xx), or the application itself answered with a non-2xx/3xx status
 * (see isDeadLastMile) -- that is a product signal for a different UI
 * surface, not a "last mile is dead" verdict. A worker is silent too: it
 * serves no HTTP at all, so whatever a domain granted before it became a
 * worker answers says nothing about its health. Absence of data must never
 * render as a health verdict either way.
 */
export interface WorkerNoHTTPNotice {
  status: number;
  checkedAt: string;
}

/**
 * The other half of the worker case. `evaluateLastMile` stays silent for a
 * worker on purpose -- its address answering 502 is not a health verdict --
 * but silence alone leaves the user with a public link that fails and no
 * word about why. Measured on production 2026-08-15, `fanvk` was a worker
 * with a healthy pod (1/1, zero restarts, VK long-polling) still carrying
 * the default hostname granted back when it served HTTP, and the console
 * said nothing at all about the 502 behind that link.
 *
 * Returns a notice only when all three hold: the snapshot declares a worker,
 * a probe actually ran, and the address answered something other than
 * 2xx/3xx. A worker whose address serves (a custom domain the user attached
 * on purpose, fronting something real) needs no explanation, and absence of
 * probe data is never rendered as a verdict either way.
 */
export function evaluateWorkerNoHTTP(summary: LastMileSummary | null | undefined): WorkerNoHTTPNotice | null {
  if (!summary) return null;
  if (summary.worker !== true) return null;
  if (summary.http_checked_at == null || summary.http_checked_at === "") return null;
  if (summary.http_status == null) return null;
  if (summary.http_status >= 200 && summary.http_status < 400) return null;
  return { status: summary.http_status, checkedAt: summary.http_checked_at };
}

/**
 * Folds the last-mile verdict into the phase string shown to the app owner.
 * Lives here, not in the page components that render it, for one reason:
 * the single frontend unit-test rig (`package.json` `test:unit`) globs
 * `lib/**` only -- there is no JSX render test in this repo -- so any rule
 * that needs a mutation-tested RED/GREEN has to sit in a plain function this
 * rig can import. The detail page (`apps/[appName]/page.tsx`) already computed
 * this inline; the list page (`apps/page.tsx`) rendered `app.phase` raw, so a
 * dead app that was still `Ready` at the pod level showed a green "Ready"
 * badge on the very first screen its owner sees. Both surfaces now call this
 * one function so they can never disagree again.
 */
export function phaseWithLastMile(
  phase: string | undefined,
  summary: LastMileSummary | null | undefined,
): string | undefined {
  if ((phase ?? "").toLowerCase() !== "ready") return phase;
  return evaluateLastMile(summary) ? "Unreachable" : phase;
}

export function evaluateLastMile(summary: LastMileSummary | null | undefined): LastMileVerdict | null {
  if (!summary) return null;
  if (summary.worker === true) return null;
  if (summary.http_checked_at == null || summary.http_checked_at === "") return null;
  if (summary.http_status == null) return null;
  if (!isDeadLastMile(summary.http_status, summary.http_reason ?? "")) return null;
  return {
    status: summary.http_status,
    reason: summary.http_reason ?? "",
    checkedAt: summary.http_checked_at,
  };
}

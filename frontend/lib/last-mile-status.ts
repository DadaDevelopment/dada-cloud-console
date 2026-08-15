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
 * Returns null whenever there is nothing honest to say: no probe has run
 * yet (`http_checked_at` absent), the probe found the address serving
 * (2xx/3xx), or the application itself answered with a non-2xx/3xx status
 * (see isDeadHTTPStatus) -- that is a product signal for a different UI
 * surface, not a "last mile is dead" verdict. Absence of data must never
 * render as a health verdict either way.
 */
export function evaluateLastMile(summary: LastMileSummary | null | undefined): LastMileVerdict | null {
  if (!summary) return null;
  if (summary.http_checked_at == null || summary.http_checked_at === "") return null;
  if (summary.http_status == null) return null;
  if (!isDeadHTTPStatus(summary.http_status)) return null;
  return {
    status: summary.http_status,
    reason: summary.http_reason ?? "",
    checkedAt: summary.http_checked_at,
  };
}

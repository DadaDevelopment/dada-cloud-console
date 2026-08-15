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
 * Same "dead" boundary the admin `live_urls` panel already uses
 * (backend/internal/api/admin_overview.go): `http_status === 0` (probe
 * attempted, target never answered -- dial error, timeout, or the probe
 * itself failed to build) or `http_status >= 400`. A 2xx/3xx is alive.
 *
 * Returns null whenever there is nothing honest to say: no probe has run
 * yet (`http_checked_at` absent) or the probe found the address serving.
 * Absence of data must never render as a health verdict either way.
 */
export function evaluateLastMile(summary: LastMileSummary | null | undefined): LastMileVerdict | null {
  if (!summary) return null;
  if (summary.http_checked_at == null || summary.http_checked_at === "") return null;
  if (summary.http_status == null) return null;
  if (summary.http_status > 0 && summary.http_status < 400) return null;
  return {
    status: summary.http_status,
    reason: summary.http_reason ?? "",
    checkedAt: summary.http_checked_at,
  };
}

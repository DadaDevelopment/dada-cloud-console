/**
 * Health of an app's public URL, separate from the app's own workload
 * health. A pod can be Running and Ready while its route is still
 * provisioning (`pending`) or has stopped resolving entirely (`failed`) --
 * the console must not conflate the two, and must not tell the owner their
 * app is broken when only the address is.
 *
 * The backend writes `url_status` onto `resource_snapshots.summary_json`
 * (see gitops-agent). Snapshots taken before that field existed simply omit
 * it, so any value outside the known set -- including missing -- normalizes
 * to `"unknown"` and the console renders exactly as it did before this
 * status existed: no new badges, no scare copy.
 */
export type AppUrlStatus = "active" | "pending" | "failed" | "unknown";

const KNOWN_STATUSES: ReadonlySet<string> = new Set(["active", "pending", "failed"]);

export function normalizeAppUrlStatus(raw: string | undefined | null): AppUrlStatus {
  if (raw && KNOWN_STATUSES.has(raw)) return raw as AppUrlStatus;
  return "unknown";
}

/**
 * Machine codes the backend may put in `domain_hostnames.status_reason` /
 * `url_reason`, mapped to console message keys under `apps.url.reason.*`.
 * Kept as one map so every code has exactly one place to add or translate.
 */
const URL_REASON_KEYS: Readonly<Record<string, string>> = {
  dns_not_pointed: "apps.url.reason.dns_not_pointed",
  cert_pending: "apps.url.reason.cert_pending",
  attach_timeout: "apps.url.reason.attach_timeout",
  route_missing: "apps.url.reason.route_missing",
  app_deleted: "apps.url.reason.app_deleted",
  awaiting_first_deploy: "apps.url.reason.awaiting_first_deploy",
};

/**
 * Message key for a `url_reason` code, or null when there is no reason to
 * show at all. Unlike domain hostname reasons (which hide unknown codes),
 * an unknown-but-present reason here must still surface -- the caller
 * should fall back to `apps.url.reason.unknown` with the raw code, never
 * drop it silently.
 */
export function appUrlReasonMessageKey(reason: string | undefined | null): string | null {
  if (!reason) return null;
  return URL_REASON_KEYS[reason] ?? null;
}

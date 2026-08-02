/**
 * Translates a domain_hostnames.status_reason machine code into user-facing
 * text.
 *
 * The backend stores a code, never prose, so the console can render it in the
 * user's language. An unknown code (a backend newer than this bundle) returns
 * null rather than the raw key, so a deploy skew shows nothing instead of
 * leaking `domains.hm.reason.something` into the UI.
 */
export function hostnameReason(
  code: string | undefined,
  t: (key: string, vars?: Record<string, string | number>) => string,
): string | null {
  if (!code) return null;
  const key = `domains.hm.reason.${code}`;
  const text = t(key);
  return text === key ? null : text;
}

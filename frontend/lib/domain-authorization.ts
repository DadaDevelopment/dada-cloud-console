export function deriveAuthorizationDomain<T extends { apex_domain: string; status: string }>(
  hostname: string,
  authorizations: T[],
): { domain: string; existing: T | null } {
  const matching = authorizations
    .filter((authorization) => hostname === authorization.apex_domain || hostname.endsWith(`.${authorization.apex_domain}`))
    .sort((left, right) => right.apex_domain.length - left.apex_domain.length);
  const existing = matching.find((authorization) => authorization.status === "verified")
    ?? matching.find((authorization) => authorization.apex_domain === hostname)
    ?? null;

  return { domain: existing?.apex_domain ?? hostname, existing };
}

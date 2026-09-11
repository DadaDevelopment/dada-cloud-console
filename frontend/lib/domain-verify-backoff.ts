/**
 * Pacing for the custom-domain TXT verification poller.
 *
 * The poller used to fire on a flat 15s interval with no cap, so a user whose
 * DNS record was not published yet generated dozens of identical failures and
 * read the same raw resolver error every time. Live data: 145 failed
 * VerifyDomainAuthorization calls against 5 successes, one user alone produced
 * 94 of them before giving up.
 *
 * The schedule grows so a slow DNS propagation costs a handful of checks rather
 * than a hundred, and stops entirely once waiting is no longer the explanation.
 */
export const VERIFY_BACKOFF_STEPS_MS = [10_000, 20_000, 40_000, 60_000, 120_000, 300_000];

/** Automatic checks stop after this many consecutive failures. */
export const VERIFY_MAX_ATTEMPTS = 10;

/**
 * Delay before the next automatic check, given how many consecutive checks
 * already failed.
 */
export function verifyDelayMs(attempt: number): number {
  if (attempt <= 0) return VERIFY_BACKOFF_STEPS_MS[0];
  const idx = Math.min(attempt, VERIFY_BACKOFF_STEPS_MS.length - 1);
  return VERIFY_BACKOFF_STEPS_MS[idx];
}

/** True once the poller has burned its budget and should hand back to the user. */
export function verifyExhausted(attempt: number): boolean {
  return attempt >= VERIFY_MAX_ATTEMPTS;
}

export type VerifyFailureKind = "not_published" | "wrong_value" | "other";

/**
 * Classifies the resolver error the backend stored, so the UI can say what the
 * user should do instead of echoing `lookup _dada-verify.example.com: no such host`.
 */
export function classifyVerifyFailure(raw: string | null | undefined): VerifyFailureKind | null {
  if (!raw) return null;
  const text = raw.toLowerCase();
  if (text.includes("no such host") || text.includes("not found") || text.includes("nxdomain")) {
    return "not_published";
  }
  if (text.includes("mismatch") || text.includes("does not match") || text.includes("no matching")) {
    return "wrong_value";
  }
  return "other";
}

/**
 * The label a DNS provider usually wants on its own, without the zone suffix.
 * Providers that reject a fully-qualified name are the most common reason a
 * record the user believes they created never resolves.
 */
export function challengeLabel(challengeHost: string, apexDomain: string): string {
  const suffix = "." + apexDomain;
  if (challengeHost.endsWith(suffix)) return challengeHost.slice(0, -suffix.length);
  return challengeHost;
}

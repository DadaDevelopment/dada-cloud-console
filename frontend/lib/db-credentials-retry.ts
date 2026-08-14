/**
 * Pure, framework-free logic behind "reveal database credentials". Kept out of
 * the database detail page so it is unit-testable with plain node:test, the
 * repo's frontend test stack, instead of only being exercisable by clicking
 * through the console.
 */

/**
 * How the console classifies a failed credential reveal. `notReady` is the
 * transient one: the managed database exists but Crossplane has not written
 * its `<appRef>-db-credentials` secret yet, so the backend answers 404 with
 * `reason=secret_not_ready`.
 */
export type DatabaseCredentialsErrorKind = "notReady" | "notConfigured" | "generic";

/**
 * Maps an API error status onto {@link DatabaseCredentialsErrorKind}.
 *
 * Branches on the numeric status alone, never on the error prose: the message
 * is human copy that is free to change, while 404/503 are the contract
 * `backend/internal/api/databases.go` actually commits to.
 */
export function credentialsErrorKind(status: number | undefined): DatabaseCredentialsErrorKind {
  if (status === 404) return "notReady";
  if (status === 503) return "notConfigured";
  return "generic";
}

/**
 * Backoff schedule for the `notReady` case, in milliseconds between attempts.
 *
 * Sized from a measured production case: a real user's `CreateServiceDatabase`
 * operation reached `Committed` at 23:04:36Z while the credentials secret only
 * materialised at 23:05:54Z - 78 seconds later, and 148 seconds after their
 * first reveal attempt. A terminal operation status is not a promise that the
 * secret exists, so the wait has to outlast that gap: this schedule spans 173
 * seconds across 7 retries.
 */
export const CREDENTIALS_PROVISION_RETRY_DELAYS_MS = [3_000, 5_000, 10_000, 20_000, 30_000, 45_000, 60_000];

/**
 * Total time {@link revealCredentialsWithProvisionRetry} will wait before it
 * gives up on a still-provisioning database.
 */
export function credentialsRetryBudgetMs(delaysMs: number[] = CREDENTIALS_PROVISION_RETRY_DELAYS_MS): number {
  return delaysMs.reduce((sum, ms) => sum + ms, 0);
}

/** Progress handed to the caller before each wait, so the UI can say how long it is still trying. */
export interface CredentialsRetryProgress {
  attempt: number;
  totalAttempts: number;
  waitMs: number;
  elapsedMs: number;
}

export type RevealCredentialsResult<T> =
  | { ok: true; value: T; retries: number }
  | { ok: false; kind: DatabaseCredentialsErrorKind; error: unknown; retries: number };

export interface RevealCredentialsOptions {
  delaysMs?: number[];
  delay?: (ms: number) => Promise<void>;
  onRetry?: (progress: CredentialsRetryProgress) => void;
  isCancelled?: () => boolean;
}

/**
 * Fetches database credentials, retrying only while the answer is
 * `notReady`.
 *
 * Without this the console asked the user to be the retry loop: the button
 * fired one request, a still-provisioning database answered 404, and the only
 * way forward was clicking again blindly with no idea whether to wait three
 * seconds or three minutes. The one production user who completed the whole
 * signup funnel in the measured window did exactly that - three failed clicks
 * across 93 seconds - before it finally worked.
 *
 * Any other failure returns straight away: a 503 means credential reveal is
 * not configured for the environment and no amount of waiting changes that.
 *
 * `delay` and `isCancelled` are injectable so tests can run the full schedule
 * instantly and so an unmounting page can stop the loop.
 */
export async function revealCredentialsWithProvisionRetry<T>(
  fetchCredentials: () => Promise<T>,
  options: RevealCredentialsOptions = {},
): Promise<RevealCredentialsResult<T>> {
  const delaysMs = options.delaysMs ?? CREDENTIALS_PROVISION_RETRY_DELAYS_MS;
  const delay = options.delay ?? ((ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms)));
  const isCancelled = options.isCancelled ?? (() => false);
  const totalAttempts = delaysMs.length + 1;

  let elapsedMs = 0;
  for (let attempt = 0; attempt < totalAttempts; attempt++) {
    try {
      return { ok: true, value: await fetchCredentials(), retries: attempt };
    } catch (err) {
      const kind = credentialsErrorKind((err as { status?: number } | undefined)?.status);
      const isLastAttempt = attempt >= delaysMs.length;
      if (kind !== "notReady" || isLastAttempt || isCancelled()) {
        return { ok: false, kind, error: err, retries: attempt };
      }
      const waitMs = delaysMs[attempt];
      options.onRetry?.({ attempt: attempt + 1, totalAttempts, waitMs, elapsedMs });
      await delay(waitMs);
      elapsedMs += waitMs;
      if (isCancelled()) return { ok: false, kind, error: err, retries: attempt + 1 };
    }
  }
  return { ok: false, kind: "generic", error: new Error("unreachable"), retries: totalAttempts };
}

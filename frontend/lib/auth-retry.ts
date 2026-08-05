/**
 * Pure, framework-free helpers behind the auth-provider's failure handling.
 * Kept out of auth-provider.tsx so they are unit-testable with plain
 * node:test (no DOM, no React renderer available in this repo's frontend
 * test stack) instead of only being exercisable by clicking through the app.
 */

export type AuthErrorCode = "token_fetch_failed" | "provider_load_failed" | "timeout";

/**
 * Retries a flaky async call with backoff before giving up. Built for
 * `sso.getAccessToken()`, whose promise can reject on a Keycloak 5xx or a
 * dropped connection (the Beget load balancer is known to drop roughly a
 * third of connections) with nothing else standing between that rejection
 * and an unresolved auth-provider promise.
 *
 * `delay` is injectable so tests can skip the real wait instead of needing
 * fake-timer infrastructure the repo's test runner does not have.
 */
export async function fetchWithRetry<T>(
  fn: () => Promise<T>,
  delaysMs: number[] = [500, 1500],
  delay: (ms: number) => Promise<void> = (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
): Promise<T> {
  let lastError: unknown;
  const attempts = delaysMs.length + 1;
  for (let attempt = 0; attempt < attempts; attempt++) {
    try {
      return await fn();
    } catch (err) {
      lastError = err;
      if (attempt < delaysMs.length) await delay(delaysMs[attempt]);
    }
  }
  throw lastError;
}

/**
 * Runs a loader (e.g. the dynamic `import("@dada/react-sso")` behind
 * `loadOidcProvider`) and turns a rejection into a typed result instead of
 * an unhandled rejection that leaves the caller's loading state stuck
 * forever.
 */
export async function loadWithFallback<T>(
  loader: () => Promise<T>,
): Promise<{ value: T; error: null } | { value: null; error: AuthErrorCode }> {
  try {
    return { value: await loader(), error: null };
  } catch {
    return { value: null, error: "provider_load_failed" };
  }
}

/**
 * Schedules a callback after `ms` and returns a canceler. This is the
 * watchdog primitive: any auth loading state that has not settled by the
 * time it fires gets forced into an error state instead of spinning forever.
 */
export function scheduleTimeout(ms: number, onTimeout: () => void): () => void {
  const id = setTimeout(onTimeout, ms);
  return () => clearTimeout(id);
}

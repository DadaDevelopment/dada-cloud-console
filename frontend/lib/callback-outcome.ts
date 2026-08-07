/**
 * Pure decision logic for the OIDC redirect landing page.
 *
 * The SSO library never surfaces a failed authorize round-trip as an error
 * state: `createOidcProvider().load()` swallows a rejected
 * `signinRedirectCallback()` and falls back to `getUser()`, which returns
 * null for a session that was never established. The provider then reports
 * `unauthenticated`, which the auth-provider translates into
 * `isLoading === false`, `token === null`, `authError === null` - exactly
 * the shape of a logged-out visitor. Anywhere else in the console that
 * shape is correct. On `/callback` it cannot be: this route is only ever
 * reached by returning from Keycloak, so arriving here without a session
 * means the round-trip failed and the page must say so instead of holding
 * a spinner forever.
 *
 * Kept framework-free so it is unit-testable with plain node:test, which is
 * the only test stack the frontend has (no DOM, no React renderer).
 */

/** Why a callback landing could not produce a session. */
export type CallbackFailureReason = "denied" | "callback";

export type CallbackVerdict =
  | { state: "pending" }
  | { state: "authenticated" }
  | { state: "failed"; reason: CallbackFailureReason; error: string | null };

/**
 * OAuth error codes that mean the person (or their identity provider) said
 * no, rather than something breaking. They deserve a different sentence:
 * "try again" is wrong advice for someone who just declined the consent
 * screen on purpose.
 */
const DENIED_ERRORS = new Set(["access_denied", "consent_required", "login_required", "interaction_required"]);

/**
 * The `error` code Keycloak put on the redirect_uri, if any. Read from the
 * raw query string because the SSO library rewrites `window.location` via
 * `history.replaceState` on the success path, and this has to work on the
 * path where it never gets that far.
 */
export function parseCallbackError(search: string): string | null {
  try {
    const value = new URLSearchParams(search.startsWith("?") ? search.slice(1) : search).get("error");
    return value && value.trim() !== "" ? value : null;
  } catch {
    return null;
  }
}

/**
 * What the callback page should render for the current auth state.
 *
 * `pending` covers both the initial load and the window between the
 * provider flipping to authenticated and the access token arriving, so a
 * healthy sign-in never flashes the failure screen. `failed` is reached
 * only once auth has settled with no token and no provider-level error,
 * which on this route is unambiguous.
 */
export function callbackVerdict(input: {
  isLoading: boolean;
  hasToken: boolean;
  hasAuthError: boolean;
  search: string;
}): CallbackVerdict {
  if (input.hasAuthError) {
    return { state: "failed", reason: "callback", error: parseCallbackError(input.search) };
  }
  if (input.isLoading) return { state: "pending" };
  if (input.hasToken) return { state: "authenticated" };
  const error = parseCallbackError(input.search);
  return {
    state: "failed",
    reason: error !== null && DENIED_ERRORS.has(error) ? "denied" : "callback",
    error,
  };
}

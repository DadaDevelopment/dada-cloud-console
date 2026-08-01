import { UserManager, WebStorageStateStore } from "oidc-client-ts";

/**
 * Keycloak required-action alias for registering a passwordless WebAuthn
 * credential (a passkey). Enabled on the master realm as an OPT-IN action, so
 * it never fires by itself — something has to ask for it, which is what
 * {@link startPasskeyEnrollment} does.
 */
export const PASSKEY_ACTION = "webauthn-register-passwordless";

/** sessionStorage key carrying the AIA outcome across the callback redirect. */
const RESULT_KEY = "dada_passkey_action_status";

/**
 * sessionStorage key marking "this browsing session just came back from an
 * authentication round-trip". Set on `/callback`, read once by the enrollment
 * prompt, cleared immediately.
 */
const FRESH_AUTH_KEY = "dada_fresh_auth";

/**
 * Outcome Keycloak reports back on the redirect_uri after an
 * Application-Initiated Action: `success`, `cancelled`, or `error`.
 */
export type PasskeyActionStatus = "success" | "cancelled" | "error";

function readStatusFromUrl(): string | null {
  if (typeof window === "undefined") return null;
  try {
    const fromUrl = new URLSearchParams(window.location.search).get("kc_action_status");
    if (fromUrl) {
      window.sessionStorage.setItem(RESULT_KEY, fromUrl);
      return fromUrl;
    }
    return window.sessionStorage.getItem(RESULT_KEY);
  } catch {
    return null;
  }
}

/**
 * Snapshot taken at module-evaluation time. `/callback` imports this module, so
 * the snapshot is taken while `?kc_action_status=…` is still in the URL —
 * before react-sso's `load()` rewrites it via history.replaceState and before
 * the callback route navigates on to the console.
 */
let pendingStatus = readStatusFromUrl();

/**
 * Re-reads `?kc_action_status=…` and parks it in sessionStorage. Idempotent:
 * once the param is gone from the URL this is a no-op. `/callback` calls it at
 * module scope so the value is captured even if that route's chunk is
 * evaluated after this module's, which is the only window in which react-sso's
 * history.replaceState could otherwise erase it first.
 */
export function capturePasskeyActionStatus(): void {
  const seen = readStatusFromUrl();
  if (seen) pendingStatus = seen;
}

/**
 * Returns the outcome of the passkey enrollment the user just came back from,
 * or null if this page load is not such a return. Reading it clears it, so the
 * result is reported exactly once.
 */
export function consumePasskeyActionStatus(): PasskeyActionStatus | null {
  const raw = pendingStatus ?? readStatusFromUrl();
  pendingStatus = null;
  try {
    window.sessionStorage.removeItem(RESULT_KEY);
  } catch {}
  if (raw === "success" || raw === "cancelled" || raw === "error") return raw;
  return null;
}

/**
 * Records that an authorize round-trip just closed: `/callback` calls this at
 * module scope, so it only ever fires on that one page load. It says nothing
 * about whether the user actually entered credentials — a reopened tab riding
 * the session cookie lands here too — so callers must pair it with
 * {@link isRecentAuthentication}.
 *
 * No-op when that round-trip was itself an Application-Initiated Action
 * (`kc_action_status` present): coming back from the passkey ceremony is an
 * answer to the offer, not a moment to make the offer again.
 */
export function markFreshAuthentication(): void {
  if (pendingStatus) return;
  try {
    window.sessionStorage.setItem(FRESH_AUTH_KEY, "1");
  } catch {}
}

/**
 * Module-scoped memo of {@link consumeFreshAuthentication}. The flag lives in
 * sessionStorage and is cleared on first read, but every call within the same
 * page load must agree — otherwise React's development double-invoke of effects
 * consumes the flag on the first pass and sees nothing on the second.
 */
let freshAuthAnswer: boolean | null = null;

/**
 * True exactly once per authentication: on the page load that follows a
 * sign-in. Lets the enrollment offer appear while the user is still in
 * credential-entering mode instead of interrupting someone who has been
 * working in the console for an hour.
 */
export function consumeFreshAuthentication(): boolean {
  if (freshAuthAnswer !== null) return freshAuthAnswer;
  let seen = false;
  try {
    seen = window.sessionStorage.getItem(FRESH_AUTH_KEY) === "1";
    window.sessionStorage.removeItem(FRESH_AUTH_KEY);
  } catch {}
  freshAuthAnswer = seen;
  return seen;
}

/**
 * How recently the user must have actually authenticated for the enrollment
 * offer to count as "at the till". Covers the Keycloak redirect plus the
 * console's own boot, with room for a slow network.
 */
const AUTH_FRESHNESS_S = 120;

/**
 * Seconds since the user last proved who they are, read from the `auth_time`
 * claim, or null when the token carries no such claim.
 *
 * `auth_time` is the moment the Keycloak SESSION was authenticated, not the
 * moment this token was minted — which is the whole point. Signing in with a
 * password or completing a sign-up starts a new session, so `auth_time` is now.
 * Merely reopening the console reuses the existing session cookie: Keycloak
 * still runs a full authorize round-trip through `/callback` and still issues a
 * fresh token, but `auth_time` stays back at the original login. Shipped by the
 * `auth_time` protocol mapper in the `basic` client scope, which `dada-console`
 * has by default and which targets the access token as well as the ID token.
 */
function secondsSinceAuthentication(accessToken: string): number | null {
  try {
    const payload = accessToken.split(".")[1];
    if (!payload) return null;
    const json = atob(payload.replace(/-/g, "+").replace(/_/g, "/"));
    const authTime = (JSON.parse(json) as { auth_time?: unknown }).auth_time;
    if (typeof authTime !== "number") return null;
    return Date.now() / 1000 - authTime;
  } catch {
    return null;
  }
}

/**
 * True when the token in hand belongs to a session the user authenticated
 * moments ago — a password sign-in or a sign-up, not a reopened tab riding an
 * existing session cookie.
 *
 * Deliberately false when the claim is missing: an offer that fails to appear
 * costs one account-menu click, while an offer that appears on every visit is
 * the exact nag this replaced.
 */
export function isRecentAuthentication(accessToken: string | null): boolean {
  if (!accessToken) return false;
  const age = secondsSinceAuthentication(accessToken);
  return age !== null && age >= 0 && age <= AUTH_FRESHNESS_S;
}

/**
 * True when this browser can create a platform passkey at all. The realm policy
 * demands `authenticatorAttachment: platform` + `userVerification: required`,
 * so offering enrollment without a built-in authenticator (Touch ID, Windows
 * Hello, Android fingerprint) only produces a dead-end WebAuthn error.
 */
export function isPasskeySupported(): boolean {
  if (typeof window === "undefined") return false;
  const pkc = (window as { PublicKeyCredential?: unknown }).PublicKeyCredential;
  return typeof pkc === "function";
}

/** True while the console runs against Keycloak; local dev auth has no passkeys. */
export function isPasskeyModeEnabled(): boolean {
  return process.env.NEXT_PUBLIC_AUTH_MODE === "oidc";
}

/**
 * Sends the user to Keycloak to enroll a passkey, then straight back into the
 * console.
 *
 * Uses Keycloak's Application-Initiated Action: an authorize request carrying
 * `kc_action=<required-action-alias>` runs that action against the already
 * authenticated session and returns to `redirect_uri` with a
 * `kc_action_status` query param. That turns enrollment into one in-app click
 * instead of "go find Account Console → Signing in → Passkey → Set up", which
 * is why the realm had zero registered passkeys despite the policy being live.
 *
 * Built on the same throwaway-UserManager trick as
 * {@link ../lib/register-redirect.startRegister}: `@dada/react-sso` exposes only
 * `login(returnTo)` and no hook for extra authorize params, but neither it nor
 * this helper overrides oidc-client-ts's default `stateStore` (localStorage),
 * so the PKCE verifier written here is read back by react-sso's own
 * UserManager on the `/callback` round-trip. Settings must therefore stay in
 * parity with `auth-provider.tsx`.
 *
 * @param returnTo in-app path to return to afterwards (defaults to /projects)
 */
export function startPasskeyEnrollment(returnTo = "/projects"): Promise<void> {
  const authority = process.env.NEXT_PUBLIC_KEYCLOAK_ISSUER ?? "";
  const clientId = process.env.NEXT_PUBLIC_OIDC_CLIENT_ID ?? "dada-console";

  const userManager = new UserManager({
    authority,
    client_id: clientId,
    redirect_uri: `${window.location.origin}/callback`,
    post_logout_redirect_uri: window.location.origin,
    response_type: "code",
    scope: "openid profile email builds:write deploy:write",
    userStore: new WebStorageStateStore({ store: window.localStorage }),
  });

  return userManager.signinRedirect({
    state: returnTo,
    extraQueryParams: { kc_action: PASSKEY_ACTION },
  });
}

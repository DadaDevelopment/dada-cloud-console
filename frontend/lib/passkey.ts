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

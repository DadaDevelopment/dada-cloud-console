import { UserManager, WebStorageStateStore } from "oidc-client-ts";

/**
 * localStorage key marking "a registration flow is in flight". Set right
 * before the redirect to Keycloak's `prompt=create` form and consumed by
 * `/callback` on return, so the callback can tell a completed sign-up apart
 * from a plain login and fire the `registration_complete` Metrika goal at
 * the moment that actually matters — not just on reaching the /register
 * page, which is all the existing page-view goal can see.
 */
export const PENDING_REGISTRATION_KEY = "dada_pending_registration";

/**
 * Sign-up entry method: `"email"` sends the visitor to Keycloak's own
 * registration form (`prompt=create`); `"yandex"` skips that form entirely
 * and drops straight into the Yandex broker login (`kc_idp_hint=yandex`),
 * since the registration form renders no IdP buttons of its own.
 */
export type RegisterMethod = "email" | "yandex";

/**
 * Builds the `extraQueryParams` for a {@link startRegister} authorize
 * request, kept as a pure function so the branching is unit-testable
 * without spinning up a {@link UserManager} or touching the DOM.
 *
 * `prompt=create` and `kc_idp_hint` are mutually exclusive: the former asks
 * Keycloak to render its registration form, the latter skips straight to a
 * broker and never reaches that form, so mixing them is meaningless.
 */
export function registerQueryParams(method: RegisterMethod): Record<string, string> {
  if (method === "yandex") return { kc_idp_hint: "yandex" };
  return { prompt: "create" };
}

/**
 * Kicks off the Keycloak sign-UP flow instead of the default sign-in form.
 *
 * `@dada/react-sso` only exposes `login(returnTo)`, which forwards `{ state }`
 * to oidc-client-ts and offers no hook for extra authorize params. This
 * helper builds a throwaway {@link UserManager} whose settings mirror the
 * react-sso OIDC provider exactly and issues the redirect with the params
 * from {@link registerQueryParams}.
 *
 * Config parity is what makes this safe: neither react-sso nor this helper
 * overrides oidc-client-ts's default `stateStore` (localStorage), so the PKCE
 * `code_verifier` written here is read back by react-sso's own UserManager on
 * the `/callback` round-trip. The user completes registration and lands
 * authenticated through the normal callback path — no second login step.
 *
 * @param returnTo in-app path to return to after auth (defaults to /projects)
 * @param method which sign-up path to take (defaults to the e-mail form)
 */
/**
 * How long a fresh {@link PENDING_REGISTRATION_KEY} marker is left alone
 * before {@link readAbandonedRegistration} will call it abandoned. The
 * visitor may still legitimately be filling in Keycloak's own form.
 */
export const ABANDONED_REGISTRATION_GRACE_MS = 90_000;

/**
 * How old a {@link PENDING_REGISTRATION_KEY} marker can get before
 * {@link readAbandonedRegistration} stops nagging about it. Past this the
 * visit is ancient history, not a bounce worth recovering.
 */
export const ABANDONED_REGISTRATION_CEILING_MS = 24 * 60 * 60 * 1000;

/**
 * Parsed shape of a {@link PENDING_REGISTRATION_KEY} value once
 * {@link startRegister} started tagging it with the chosen method.
 */
interface PendingRegistrationMarker {
  method: RegisterMethod;
  startedAt: number;
}

/**
 * Serializes the marker written to {@link PENDING_REGISTRATION_KEY}.
 *
 * Kept on its own line as `<timestamp>:<method>` rather than JSON so an old
 * bare-timestamp value already sitting in a visitor's localStorage (written
 * by a build before this marker carried a method) still parses as a valid
 * timestamp by {@link parsePendingRegistration} -- just without a method.
 */
function serializePendingRegistration(method: RegisterMethod, now: number): string {
  return `${now}:${method}`;
}

/**
 * Parses a {@link PENDING_REGISTRATION_KEY} value written by either the
 * current format (`"<timestamp>:<method>"`) or the legacy bare-timestamp
 * format that shipped before this marker carried a method. Returns null for
 * anything that isn't a finite timestamp.
 */
function parsePendingRegistration(raw: string): PendingRegistrationMarker | null {
  const sepIndex = raw.indexOf(":");
  const timestampPart = sepIndex === -1 ? raw : raw.slice(0, sepIndex);
  const startedAt = Number(timestampPart);
  if (!Number.isFinite(startedAt)) return null;
  const methodPart = sepIndex === -1 ? "" : raw.slice(sepIndex + 1);
  const method: RegisterMethod = methodPart === "yandex" ? "yandex" : "email";
  return { method, startedAt };
}

/**
 * Pure decision function behind the "you didn't finish signing up" recovery
 * banner on `/register`. Given the raw {@link PENDING_REGISTRATION_KEY}
 * value and the current time, decides whether the visitor is looking at an
 * abandoned registration worth surfacing.
 *
 * Returns null when: there is no marker, it fails to parse, it is younger
 * than {@link ABANDONED_REGISTRATION_GRACE_MS} (still plausibly on
 * Keycloak's own form), or older than {@link ABANDONED_REGISTRATION_CEILING_MS}
 * (too stale to be worth a nag).
 */
export function readAbandonedRegistration(
  raw: string | null,
  now: number,
): { method: RegisterMethod; ageMs: number } | null {
  if (!raw) return null;
  const parsed = parsePendingRegistration(raw);
  if (!parsed) return null;
  const ageMs = now - parsed.startedAt;
  if (!Number.isFinite(ageMs) || ageMs < ABANDONED_REGISTRATION_GRACE_MS) return null;
  if (ageMs > ABANDONED_REGISTRATION_CEILING_MS) return null;
  return { method: parsed.method, ageMs };
}

export function startRegister(returnTo = "/projects", method: RegisterMethod = "email"): Promise<void> {
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

  try {
    window.localStorage.setItem(PENDING_REGISTRATION_KEY, serializePendingRegistration(method, Date.now()));
  } catch {}

  return userManager.signinRedirect({
    state: returnTo,
    extraQueryParams: registerQueryParams(method),
  });
}

/**
 * Kicks off the ordinary Keycloak sign-IN flow, returning to `returnTo`.
 *
 * The auth context's `login()` bridges to `@dada/react-sso` with no argument,
 * so it always lands back on the default page. Campaign links need the return
 * path preserved: a promo recipient who is bounced to Keycloak must come back
 * to the promo URL, or the token they were mailed is silently lost. This
 * mirrors {@link startRegister}'s config exactly — same client, same scopes,
 * same localStorage state store — so react-sso's own UserManager completes the
 * `/callback` round-trip, and only omits `prompt=create`: these recipients
 * already have accounts and must see the sign-in form, not the sign-up one.
 *
 * @param returnTo in-app path to return to after auth (defaults to /projects)
 * @param forcePrompt re-ask for credentials even when a Keycloak session is
 *   already live. Used when the current session is the wrong identity: a plain
 *   redirect would be waved straight through by the session cookie and land
 *   back on the same refusal.
 */
export function startLogin(returnTo = "/projects", forcePrompt = false): Promise<void> {
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
    extraQueryParams: forcePrompt ? { prompt: "login" } : {},
  });
}

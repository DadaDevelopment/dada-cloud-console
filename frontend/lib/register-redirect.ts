import { UserManager, WebStorageStateStore } from "oidc-client-ts";

/**
 * Kicks off the Keycloak sign-UP flow (registration form) instead of the
 * default sign-in form.
 *
 * `@dada/react-sso` only exposes `login(returnTo)`, which forwards `{ state }`
 * to oidc-client-ts and offers no hook for extra authorize params. Keycloak
 * 20+ renders its registration page when the authorize request carries
 * `prompt=create`, so this helper builds a throwaway {@link UserManager} whose
 * settings mirror the react-sso OIDC provider exactly and issues the redirect
 * with that param.
 *
 * Config parity is what makes this safe: neither react-sso nor this helper
 * overrides oidc-client-ts's default `stateStore` (localStorage), so the PKCE
 * `code_verifier` written here is read back by react-sso's own UserManager on
 * the `/callback` round-trip. The user completes registration and lands
 * authenticated through the normal callback path — no second login step.
 *
 * @param returnTo in-app path to return to after auth (defaults to /projects)
 */
export function startRegister(returnTo = "/projects"): Promise<void> {
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
    extraQueryParams: { prompt: "create" },
  });
}

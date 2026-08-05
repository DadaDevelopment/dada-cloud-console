"use client";
import { createContext, useContext, useEffect, useState } from "react";
import type { User } from "./types";
import { setTokenGetter } from "./api";
import { publishUid } from "./uid-cookie";
import { fetchWithRetry, loadWithFallback, scheduleTimeout, type AuthErrorCode } from "./auth-retry";

export type { AuthErrorCode } from "./auth-retry";

interface AuthContextValue {
  user: User | null;
  token: string | null;
  login: (token?: string, user?: User) => void;
  logout: () => void;
  isLoading: boolean;
  /**
   * Set once auth state cannot settle on its own: the access-token fetch
   * failed after retries, the OIDC provider chunk failed to load, or the
   * loading watchdog fired. Callers must show a dead-end screen with a
   * manual retry/logout, never redirect to /login while this is set -
   * /login auto-starts a Keycloak redirect for a logged-out user, which
   * would recreate the same hang under a live SSO session.
   */
  authError: AuthErrorCode | null;
}

/**
 * How long the SSO provider is allowed to stay in `loading` before the
 * watchdog forces an error state. Set well above the ~75s cold-start the
 * console backend itself can take (this watchdog is not on that path) and
 * comfortably above the ~3.5s worst case of the token-fetch retry loop
 * below; 30s is long enough that no real network round-trip should hit it,
 * short enough that a genuinely stuck promise does not leave the user
 * staring at a spinner indefinitely.
 */
const AUTH_WATCHDOG_TIMEOUT_MS = 30_000;

/** Backoff schedule for retrying a failed `sso.getAccessToken()` call. */
const TOKEN_FETCH_RETRY_DELAYS_MS = [500, 1500];

export const AuthContext = createContext<AuthContextValue | null>(null);

const AUTH_CHANGE_EVENT = "dada-auth-change";

function readAuthFromStorage() {
  const storedToken = localStorage.getItem("dada_token");
  const storedUser = localStorage.getItem("dada_user");
  if (!storedToken || !storedUser) {
    return { user: null, token: null, isLoading: false };
  }
  try {
    return {
      user: JSON.parse(storedUser) as User,
      token: storedToken,
      isLoading: false,
    };
  } catch {
    return { user: null, token: null, isLoading: false };
  }
}

function LocalAuthProvider({ children }: { children: React.ReactNode }) {
  const [auth, setAuth] = useState<{ user: User | null; token: string | null; isLoading: boolean }>(
    { user: null, token: null, isLoading: true },
  );

  useEffect(() => {
    // Initial hydration from localStorage. The state-from-storage pattern
    // is exactly what the rule's storage-event branch is for; the initial
    // read has to live in an effect because localStorage isn't available
    // during SSR. This is intentional, not the cascading-render footgun
    // the rule is guarding against.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setAuth(readAuthFromStorage());

    const handler = () => setAuth(readAuthFromStorage());
    window.addEventListener("storage", handler);
    window.addEventListener(AUTH_CHANGE_EVENT, handler as EventListener);
    return () => {
      window.removeEventListener("storage", handler);
      window.removeEventListener(AUTH_CHANGE_EVENT, handler as EventListener);
    };
  }, []);

  // Keep the fleet-wide dada_uid cookie in sync with the current user across
  // hydration, login, logout, and cross-tab storage events.
  useEffect(() => {
    publishUid(auth.user?.id ?? null);
  }, [auth.user?.id]);

  function login(newToken?: string, newUser?: User) {
    if (!newToken || !newUser) return;
    localStorage.setItem("dada_token", newToken);
    localStorage.setItem("dada_user", JSON.stringify(newUser));
    window.dispatchEvent(new Event(AUTH_CHANGE_EVENT));
  }

  function logout() {
    localStorage.removeItem("dada_token");
    localStorage.removeItem("dada_user");
    window.dispatchEvent(new Event(AUTH_CHANGE_EVENT));
  }

  return (
    <AuthContext.Provider value={{ ...auth, login, logout, authError: null }}>
      {children}
    </AuthContext.Provider>
  );
}

// ----- OIDC mode -----

const AUTH_MODE = process.env.NEXT_PUBLIC_AUTH_MODE;
const OIDC_AUTHORITY = process.env.NEXT_PUBLIC_KEYCLOAK_ISSUER ?? "";
const OIDC_CLIENT_ID = process.env.NEXT_PUBLIC_OIDC_CLIENT_ID ?? "dada-console";

// Lazy import to avoid bundling oidc-client-ts in local mode.
let _OidcAuthProvider: React.ComponentType<{ children: React.ReactNode }> | null = null;

async function loadOidcProvider(): Promise<React.ComponentType<{ children: React.ReactNode }>> {
  if (_OidcAuthProvider) return _OidcAuthProvider;
  const { SsoProvider, useSso } = await import("@dada/react-sso");

  function OidcBridge({ children }: { children: React.ReactNode }) {
    const sso = useSso();
    const [token, setToken] = useState<string | null>(null);
    /**
     * Tracks whether the access-token fetch has settled for the current
     * status. The token is fetched asynchronously AFTER sso.status flips to
     * "authenticated", so without this there is a render where
     * status==="authenticated", isLoading===false, but token===null - and
     * the console route guard reads that as logged-out and bounces to
     * /login on every page refresh.
     */
    const [tokenReady, setTokenReady] = useState(false);
    /**
     * Set when the token fetch exhausts its retries, the loading watchdog
     * below fires, or (via {@link OidcAuthProviderLazy}) the provider chunk
     * itself failed to load. A non-null value takes over rendering: it is
     * the difference between "still loading" and "gave up", so callers must
     * check it before treating `isLoading === false` as logged-out.
     */
    const [authError, setAuthError] = useState<AuthErrorCode | null>(null);

    useEffect(() => {
      setTokenGetter(() => sso.getAccessToken());
      return () => setTokenGetter(null);
    }, [sso]);

    useEffect(() => {
      let active = true;
      if (sso.status === "authenticated") {
        fetchWithRetry(() => sso.getAccessToken(), TOKEN_FETCH_RETRY_DELAYS_MS)
          .then((t) => {
            if (!active) return;
            setToken(t);
            setTokenReady(true);
            setAuthError(null);
          })
          .catch(() => {
            if (!active) return;
            setTokenReady(true);
            setAuthError("token_fetch_failed");
          });
      } else {
        // eslint-disable-next-line react-hooks/set-state-in-effect
        setToken(null);
        setTokenReady(sso.status !== "loading");
      }
      return () => {
        active = false;
      };
    }, [sso, sso.status]);

    /**
     * Publishes the internal, non-PII id (OIDC sub) into the fleet-wide
     * dada_uid cookie so same-domain static frontends can bind it to
     * Yandex.Metrika. Cleared when the principal goes away (logout /
     * unauthenticated).
     */
    useEffect(() => {
      publishUid(sso.principal?.sub ?? null);
    }, [sso.principal?.sub]);

    const isLoading =
      authError === null && (sso.status === "loading" || (sso.status === "authenticated" && !tokenReady));

    useEffect(() => {
      if (!isLoading) return undefined;
      return scheduleTimeout(AUTH_WATCHDOG_TIMEOUT_MS, () => {
        setAuthError((prev) => prev ?? "timeout");
        setTokenReady(true);
      });
    }, [isLoading]);

    const user: User | null = sso.principal
      ? {
          id: sso.principal.sub ?? "",
          username: sso.principal.username ?? "",
          email: sso.principal.email ?? "",
          display_name: sso.principal.name ?? sso.principal.username ?? "",
        }
      : null;

    const value: AuthContextValue = {
      user: authError ? null : user,
      token: authError ? null : token,
      isLoading,
      authError,
      login: () => void sso.login(),
      logout: () => void sso.logout(),
    };

    return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
  }

  function OidcAuthProvider({ children }: { children: React.ReactNode }) {
    return (
      <SsoProvider
        config={{
          mode: "oidc",
          oidc: {
            authority: OIDC_AUTHORITY,
            clientId: OIDC_CLIENT_ID,
            scope: "openid profile email builds:write deploy:write",
          },
          rolesClientId: OIDC_CLIENT_ID,
        }}
      >
        <OidcBridge>{children}</OidcBridge>
      </SsoProvider>
    );
  }

  _OidcAuthProvider = OidcAuthProvider;
  return OidcAuthProvider;
}

/**
 * Standard Keycloak end-session redirect. Used only when the OIDC provider
 * chunk itself never loaded, so `sso.logout()` does not exist yet - this is
 * the one path where breaking a stuck SSO session has to be done without the
 * library.
 */
function buildKeycloakLogoutUrl(): string {
  const redirectTo = typeof window !== "undefined" ? `${window.location.origin}/` : "/";
  return `${OIDC_AUTHORITY}/protocol/openid-connect/logout?client_id=${encodeURIComponent(OIDC_CLIENT_ID)}&post_logout_redirect_uri=${encodeURIComponent(redirectTo)}`;
}

function OidcAuthProviderLazy({ children }: { children: React.ReactNode }) {
  const [Provider, setProvider] = useState<React.ComponentType<{ children: React.ReactNode }> | null>(null);
  const [loadError, setLoadError] = useState<AuthErrorCode | null>(null);

  useEffect(() => {
    let active = true;
    loadWithFallback(loadOidcProvider).then((result) => {
      if (!active) return;
      if (result.error) setLoadError(result.error);
      else setProvider(() => result.value);
    });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    if (Provider || loadError) return undefined;
    return scheduleTimeout(AUTH_WATCHDOG_TIMEOUT_MS, () => setLoadError("timeout"));
  }, [Provider, loadError]);

  if (loadError) {
    return (
      <AuthContext.Provider
        value={{
          user: null,
          token: null,
          isLoading: false,
          authError: loadError,
          login: () => window.location.reload(),
          logout: () => {
            window.location.href = buildKeycloakLogoutUrl();
          },
        }}
      >
        {children}
      </AuthContext.Provider>
    );
  }

  if (!Provider) {
    return (
      <AuthContext.Provider
        value={{ user: null, token: null, isLoading: true, authError: null, login: () => {}, logout: () => {} }}
      >
        {children}
      </AuthContext.Provider>
    );
  }

  return <Provider>{children}</Provider>;
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  if (AUTH_MODE === "oidc") {
    return <OidcAuthProviderLazy>{children}</OidcAuthProviderLazy>;
  }
  return <LocalAuthProvider>{children}</LocalAuthProvider>;
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}

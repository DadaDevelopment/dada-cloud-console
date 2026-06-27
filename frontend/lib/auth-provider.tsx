"use client";
import { createContext, useContext, useEffect, useState } from "react";
import type { User } from "./types";
import { setTokenGetter } from "./api";

interface AuthContextValue {
  user: User | null;
  token: string | null;
  login: (token?: string, user?: User) => void;
  logout: () => void;
  isLoading: boolean;
}

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
    <AuthContext.Provider value={{ ...auth, login, logout }}>
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
    // Tracks whether the access-token fetch has settled for the current status.
    // The token is fetched asynchronously AFTER sso.status flips to
    // "authenticated", so without this there is a render where
    // status==="authenticated", isLoading===false, but token===null — and the
    // console route guard reads that as logged-out and bounces to /login on
    // every page refresh.
    const [tokenReady, setTokenReady] = useState(false);

    useEffect(() => {
      setTokenGetter(() => sso.getAccessToken());
      return () => setTokenGetter(null);
    }, [sso]);

    useEffect(() => {
      let active = true;
      if (sso.status === "authenticated") {
        sso.getAccessToken().then((t) => {
          if (!active) return;
          setToken(t);
          setTokenReady(true);
        });
      } else {
        // eslint-disable-next-line react-hooks/set-state-in-effect
        setToken(null);
        // "loading" is still pending; only unauthenticated/error are settled.
        setTokenReady(sso.status !== "loading");
      }
      return () => {
        active = false;
      };
    }, [sso, sso.status]);

    const user: User | null = sso.principal
      ? {
          id: sso.principal.sub ?? "",
          username: sso.principal.username ?? "",
          email: sso.principal.email ?? "",
          display_name: sso.principal.name ?? sso.principal.username ?? "",
        }
      : null;

    const value: AuthContextValue = {
      user,
      token,
      isLoading: sso.status === "loading" || (sso.status === "authenticated" && !tokenReady),
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

function OidcAuthProviderLazy({ children }: { children: React.ReactNode }) {
  const [Provider, setProvider] = useState<React.ComponentType<{ children: React.ReactNode }> | null>(null);

  useEffect(() => {
    loadOidcProvider().then((P) => setProvider(() => P));
  }, []);

  if (!Provider) {
    return (
      <AuthContext.Provider value={{ user: null, token: null, isLoading: true, login: () => {}, logout: () => {} }}>
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

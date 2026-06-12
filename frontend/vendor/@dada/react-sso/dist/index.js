import { UserManager, WebStorageStateStore } from 'oidc-client-ts';
import { createContext, useRef, useState, useCallback, useEffect, useMemo, useContext } from 'react';
import { jsx, Fragment, jsxs } from 'react/jsx-runtime';

// src/Principal.ts
function strList(value) {
  return Array.isArray(value) ? value.filter((v) => typeof v === "string") : [];
}
function rolesFromClaims(claims, clientId) {
  const roles = [];
  const realm = claims["realm_access"];
  if (realm && typeof realm === "object") {
    roles.push(...strList(realm["roles"]));
  }
  const resource = claims["resource_access"];
  if (resource && typeof resource === "object") {
    const client = resource[clientId];
    if (client && typeof client === "object") {
      roles.push(...strList(client["roles"]));
    }
  }
  return roles;
}
function principalFromClaims(claims, clientId) {
  return {
    sub: claims["sub"] ?? null,
    username: claims["preferred_username"] ?? null,
    email: claims["email"] ?? null,
    name: claims["name"] ?? null,
    groups: strList(claims["groups"]),
    roles: rolesFromClaims(claims, clientId),
    source: "oidc",
    raw: claims
  };
}
function principalFromUserinfo(info) {
  return {
    sub: info.user ?? null,
    username: info.preferredUsername ?? info.user ?? null,
    email: info.email ?? null,
    name: null,
    groups: strList(info.groups),
    roles: [],
    source: "proxy",
    raw: info
  };
}

// src/config.ts
function resolveProxyBasePath(config) {
  return config.proxy?.basePath ?? "/oauth2";
}
function resolveRolesClientId(config) {
  return config.rolesClientId ?? "service-client";
}

// src/providers/oidcProvider.ts
function safeReturnTo(value) {
  if (!value) return null;
  if (!value.startsWith("/") || value.startsWith("//")) return null;
  return value;
}
function createOidcProvider(config) {
  const o = config.oidc;
  if (!o) throw new Error("@dada/react-sso: oidc mode requires config.oidc");
  const rolesClient = resolveRolesClientId(config);
  const redirectUri = o.redirectUri ?? `${window.location.origin}/callback`;
  const userManager = new UserManager({
    authority: o.authority,
    client_id: o.clientId,
    redirect_uri: redirectUri,
    post_logout_redirect_uri: o.postLogoutRedirectUri ?? window.location.origin,
    response_type: "code",
    scope: o.scope ?? "openid profile email",
    automaticSilentRenew: true,
    silent_redirect_uri: o.silentRedirectUri,
    userStore: new WebStorageStateStore({ store: window.localStorage })
  });
  return {
    async load() {
      const search = window.location.search;
      if (search.includes("code=") || search.includes("error=")) {
        try {
          const user2 = await userManager.signinRedirectCallback();
          const state = typeof user2?.state === "string" ? user2.state : null;
          const fallbackPath = new URL(redirectUri, window.location.origin).pathname;
          window.history.replaceState({}, document.title, safeReturnTo(state) ?? fallbackPath);
          return user2 ? principalFromClaims(user2.profile, rolesClient) : null;
        } catch {
        }
      }
      const user = await userManager.getUser();
      if (!user || user.expired) return null;
      return principalFromClaims(user.profile, rolesClient);
    },
    async login(returnTo) {
      await userManager.signinRedirect(returnTo ? { state: returnTo } : void 0);
    },
    async logout() {
      await userManager.signoutRedirect();
    },
    async getAccessToken() {
      const user = await userManager.getUser();
      return user && !user.expired ? user.access_token : null;
    },
    onExpired(cb) {
      userManager.events.addAccessTokenExpired(cb);
      return () => userManager.events.removeAccessTokenExpired(cb);
    }
  };
}

// src/providers/proxyProvider.ts
function createProxyProvider(config) {
  const base = resolveProxyBasePath(config);
  return {
    async load() {
      const res = await fetch(`${base}/userinfo`, { credentials: "include" });
      if (!res.ok) return null;
      const info = await res.json();
      return principalFromUserinfo(info);
    },
    login(returnTo) {
      const rd = encodeURIComponent(returnTo ?? window.location.href);
      window.location.assign(`${base}/start?rd=${rd}`);
    },
    logout(returnTo) {
      const rd = encodeURIComponent(returnTo ?? window.location.origin);
      window.location.assign(`${base}/sign_out?rd=${rd}`);
    },
    async getAccessToken() {
      return null;
    }
  };
}

// src/providers/factory.ts
function createProvider(config) {
  return config.mode === "oidc" ? createOidcProvider(config) : createProxyProvider(config);
}
var SsoContext = createContext(null);
function SsoProvider({ config, children, provider }) {
  const providerRef = useRef(null);
  if (providerRef.current === null) {
    providerRef.current = provider ?? createProvider(config);
  }
  const [status, setStatus] = useState("loading");
  const [principal, setPrincipal] = useState(null);
  const reload = useCallback(async () => {
    try {
      const p = await providerRef.current.load();
      setPrincipal(p);
      setStatus(p ? "authenticated" : "unauthenticated");
    } catch {
      setPrincipal(null);
      setStatus("error");
    }
  }, []);
  useEffect(() => {
    let active = true;
    providerRef.current.load().then((p) => {
      if (!active) return;
      setPrincipal(p);
      setStatus(p ? "authenticated" : "unauthenticated");
    }).catch(() => {
      if (active) {
        setPrincipal(null);
        setStatus("error");
      }
    });
    return () => {
      active = false;
    };
  }, []);
  useEffect(() => {
    const off = providerRef.current.onExpired?.(() => {
      setPrincipal(null);
      setStatus("unauthenticated");
    });
    return off;
  }, []);
  const value = useMemo(
    () => ({
      status,
      principal,
      login: (returnTo) => void providerRef.current.login(returnTo),
      logout: (returnTo) => void providerRef.current.logout(returnTo),
      reload,
      getAccessToken: () => providerRef.current.getAccessToken(),
      hasGroup: (group) => principal?.groups.includes(group) ?? false,
      hasRole: (role) => principal?.roles.includes(role) ?? false
    }),
    [status, principal, reload]
  );
  return /* @__PURE__ */ jsx(SsoContext.Provider, { value, children });
}
function useSso() {
  const ctx = useContext(SsoContext);
  if (!ctx) throw new Error("useSso must be used within a <SsoProvider>");
  return ctx;
}
function RequireAuth({ children, fallback = null }) {
  const { status, login } = useSso();
  const fired = useRef(false);
  useEffect(() => {
    if (status === "unauthenticated" && !fired.current) {
      fired.current = true;
      login();
    }
  }, [status, login]);
  if (status === "authenticated") return /* @__PURE__ */ jsx(Fragment, { children });
  return /* @__PURE__ */ jsx(Fragment, { children: fallback });
}
function RequireGroups({ groups, children, matchAll = false, fallback, forbidden = null }) {
  const { principal } = useSso();
  const have = new Set(principal?.groups ?? []);
  const ok = matchAll ? groups.every((g) => have.has(g)) : groups.some((g) => have.has(g));
  return /* @__PURE__ */ jsx(RequireAuth, { fallback, children: ok ? /* @__PURE__ */ jsx(Fragment, { children }) : /* @__PURE__ */ jsx(Fragment, { children: forbidden }) });
}
function RequireRoles({ roles, children, matchAll = false, fallback, forbidden = null }) {
  const { principal } = useSso();
  const have = new Set(principal?.roles ?? []);
  const ok = matchAll ? roles.every((r) => have.has(r)) : roles.some((r) => have.has(r));
  return /* @__PURE__ */ jsx(RequireAuth, { fallback, children: ok ? /* @__PURE__ */ jsx(Fragment, { children }) : /* @__PURE__ */ jsx(Fragment, { children: forbidden }) });
}

// src/http/attachAuth.ts
function isSameOrigin(input) {
  try {
    const href = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    return new URL(href, window.location.origin).origin === window.location.origin;
  } catch {
    return true;
  }
}
function createAuthFetch(options) {
  const impl = options.fetchImpl ?? fetch;
  return (async (input, init = {}) => {
    const headers = new Headers(init.headers);
    const token = await options.getAccessToken();
    if (token && isSameOrigin(input)) headers.set("Authorization", `Bearer ${token}`);
    const res = await impl(input, { ...init, headers, credentials: init.credentials ?? "include" });
    if (res.status === 401) options.onUnauthorized?.();
    return res;
  });
}
function attachAxios(instance, options) {
  instance.interceptors.request.use(async (config) => {
    const token = await options.getAccessToken();
    if (token) {
      const headers = config.headers;
      if (headers && typeof headers.set === "function") {
        headers.set("Authorization", `Bearer ${token}`);
      } else {
        config.headers = config.headers ?? {};
        config.headers.Authorization = `Bearer ${token}`;
      }
    }
    return config;
  });
  instance.interceptors.response.use(
    (response) => response,
    (error) => {
      const status = error?.response?.status;
      if (status === 401) options.onUnauthorized?.();
      return Promise.reject(error);
    }
  );
}
function Spinner() {
  return /* @__PURE__ */ jsx("div", { "data-dada-sso-spinner": true, "aria-label": "loading", role: "status" });
}
function SsoGate({ children, loading = /* @__PURE__ */ jsx(Spinner, {}), fallback = null }) {
  const { status } = useSso();
  if (status === "loading") return /* @__PURE__ */ jsx(Fragment, { children: loading });
  if (status === "authenticated") return /* @__PURE__ */ jsx(Fragment, { children });
  return /* @__PURE__ */ jsx(Fragment, { children: fallback });
}
function LoginButton({ children = "\u0412\u043E\u0439\u0442\u0438", returnTo, className }) {
  const { login } = useSso();
  return /* @__PURE__ */ jsx("button", { type: "button", className, onClick: () => login(returnTo), children });
}
function UserMenu({ className, logoutLabel = "\u0412\u044B\u0439\u0442\u0438" }) {
  const { status, principal, logout } = useSso();
  if (status !== "authenticated" || !principal) return null;
  return /* @__PURE__ */ jsxs("div", { "data-dada-sso-usermenu": true, className, children: [
    /* @__PURE__ */ jsx("span", { "data-dada-sso-username": true, children: principal.username ?? principal.email ?? principal.sub }),
    /* @__PURE__ */ jsx("button", { type: "button", onClick: () => logout(), children: logoutLabel })
  ] });
}

export { LoginButton, RequireAuth, RequireGroups, RequireRoles, Spinner, SsoGate, SsoProvider, UserMenu, attachAxios, createAuthFetch, createOidcProvider, createProvider, createProxyProvider, principalFromClaims, principalFromUserinfo, rolesFromClaims, useSso };
//# sourceMappingURL=index.js.map
//# sourceMappingURL=index.js.map
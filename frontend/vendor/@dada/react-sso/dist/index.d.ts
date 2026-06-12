import * as react from 'react';
import { ReactNode } from 'react';

interface Principal {
    sub: string | null;
    username: string | null;
    email: string | null;
    name: string | null;
    groups: string[];
    roles: string[];
    source: 'proxy' | 'oidc';
    raw: Record<string, unknown>;
}
interface ProxyUserinfo {
    user?: string;
    email?: string;
    preferredUsername?: string;
    groups?: string[];
}
type Claims = Record<string, unknown>;
declare function rolesFromClaims(claims: Claims, clientId: string): string[];
declare function principalFromClaims(claims: Claims, clientId: string): Principal;
declare function principalFromUserinfo(info: ProxyUserinfo): Principal;

type SsoMode = 'proxy' | 'oidc';
interface ProxyConfig {
    basePath?: string;
}
interface OidcConfig {
    authority: string;
    clientId: string;
    redirectUri?: string;
    postLogoutRedirectUri?: string;
    scope?: string;
    silentRedirectUri?: string;
}
interface SsoConfig {
    mode: SsoMode;
    proxy?: ProxyConfig;
    oidc?: OidcConfig;
    rolesClientId?: string;
}

interface SessionProvider {
    /** Resolve current identity (handles oidc callback). Null if unauthenticated. */
    load(): Promise<Principal | null>;
    /** Begin login (redirect). */
    login(returnTo?: string): void | Promise<void>;
    /** Begin logout (redirect). */
    logout(returnTo?: string): void | Promise<void>;
    /** Access token for Authorization header (null in proxy mode). */
    getAccessToken(): Promise<string | null>;
    /** Optional: subscribe to token-expired; returns an unsubscribe fn. */
    onExpired?(cb: () => void): () => void;
}

declare function createProvider(config: SsoConfig): SessionProvider;

declare function createProxyProvider(config: SsoConfig): SessionProvider;

declare function createOidcProvider(config: SsoConfig): SessionProvider;

interface SsoProviderProps {
    config: SsoConfig;
    children: ReactNode;
    /** Inject a provider (tests / custom). Defaults to createProvider(config). */
    provider?: SessionProvider;
}
declare function SsoProvider({ config, children, provider }: SsoProviderProps): react.JSX.Element;

type SsoStatus = 'loading' | 'authenticated' | 'unauthenticated' | 'error';
interface SsoContextValue {
    status: SsoStatus;
    principal: Principal | null;
    login: (returnTo?: string) => void;
    logout: (returnTo?: string) => void;
    reload: () => Promise<void>;
    getAccessToken: () => Promise<string | null>;
    hasGroup: (group: string) => boolean;
    hasRole: (role: string) => boolean;
}

declare function useSso(): SsoContextValue;

interface RequireAuthProps {
    children: ReactNode;
    /** Shown while loading, on error, or while the login redirect is in flight. */
    fallback?: ReactNode;
}
declare function RequireAuth({ children, fallback }: RequireAuthProps): react.JSX.Element;

interface RequireGroupsProps {
    groups: string[];
    children: ReactNode;
    matchAll?: boolean;
    fallback?: ReactNode;
    forbidden?: ReactNode;
}
declare function RequireGroups({ groups, children, matchAll, fallback, forbidden }: RequireGroupsProps): react.JSX.Element;

interface RequireRolesProps {
    roles: string[];
    children: ReactNode;
    matchAll?: boolean;
    fallback?: ReactNode;
    forbidden?: ReactNode;
}
declare function RequireRoles({ roles, children, matchAll, fallback, forbidden }: RequireRolesProps): react.JSX.Element;

interface AuthHttpOptions {
    getAccessToken: () => Promise<string | null>;
    /** Called when a response is 401 (e.g. pass SsoProvider's login). */
    onUnauthorized?: () => void;
}
interface AuthFetchOptions extends AuthHttpOptions {
    fetchImpl?: typeof fetch;
}
declare function createAuthFetch(options: AuthFetchOptions): typeof fetch;
type AxiosHeadersLike = Record<string, string> & {
    set?: (name: string, value: string) => void;
};
interface AxiosLike {
    interceptors: {
        request: {
            use: (fn: (config: {
                headers?: AxiosHeadersLike;
            }) => unknown) => unknown;
        };
        response: {
            use: (onFulfilled: (r: unknown) => unknown, onRejected: (e: unknown) => unknown) => unknown;
        };
    };
}
declare function attachAxios(instance: AxiosLike, options: AuthHttpOptions): void;

declare function Spinner(): react.JSX.Element;

interface SsoGateProps {
    children: ReactNode;
    /** Shown while bootstrapping. Defaults to <Spinner/>. */
    loading?: ReactNode;
    /** Shown when unauthenticated. Defaults to null. */
    fallback?: ReactNode;
}
declare function SsoGate({ children, loading, fallback }: SsoGateProps): react.JSX.Element;

interface LoginButtonProps {
    children?: ReactNode;
    returnTo?: string;
    className?: string;
}
declare function LoginButton({ children, returnTo, className }: LoginButtonProps): react.JSX.Element;

interface UserMenuProps {
    className?: string;
    logoutLabel?: string;
}
declare function UserMenu({ className, logoutLabel }: UserMenuProps): react.JSX.Element | null;

export { type AuthFetchOptions, type AuthHttpOptions, LoginButton, type OidcConfig, type Principal, type ProxyConfig, type ProxyUserinfo, RequireAuth, RequireGroups, RequireRoles, type SessionProvider, Spinner, type SsoConfig, type SsoContextValue, SsoGate, type SsoMode, SsoProvider, type SsoProviderProps, type SsoStatus, UserMenu, attachAxios, createAuthFetch, createOidcProvider, createProvider, createProxyProvider, principalFromClaims, principalFromUserinfo, rolesFromClaims, useSso };

"use client";
import { useEffect, useRef, useState, FormEvent } from "react";
import { useRouter } from "next/navigation";
import { authApi } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { AuthErrorScreen } from "@/components/shell/auth-error-screen";
import { startLogin } from "@/lib/register-redirect";
import { rememberSource } from "@/lib/metrika";

const AUTH_MODE = process.env.NEXT_PUBLIC_AUTH_MODE;
const LOGIN_SEARCH = typeof window === "undefined" ? new URLSearchParams() : new URLSearchParams(window.location.search);

function sanitizeReturnTo(value: string | null): string {
  if (!value || !value.startsWith("/") || value.startsWith("//")) return "/projects";
  return value;
}

/**
 * OIDC entry point. Instead of an intermediate "Sign in with Keycloak" card,
 * it kicks off the Keycloak redirect automatically as soon as auth state is
 * known and the user is unauthenticated. When Keycloak already has an SSO
 * session, the round-trip returns instantly authenticated (no password
 * prompt), so a logged-in Keycloak user never sees a login screen here. When
 * there is no session, the user lands directly on Keycloak's own login form.
 */
function OidcLoginPage() {
  const router = useRouter();
  const { isLoading, token, authError, logout } = useAuth();
  const startedRef = useRef(false);
  const returnTo = sanitizeReturnTo(LOGIN_SEARCH.get("returnTo") ?? LOGIN_SEARCH.get("next"));
  const source = LOGIN_SEARCH.get("utm_source") ?? "direct";

  useEffect(() => {
    if (isLoading || authError) return;
    if (token) {
      router.replace("/projects");
      return;
    }
    if (startedRef.current) return;
    startedRef.current = true;
    rememberSource(source);
    void startLogin(returnTo);
  }, [isLoading, token, authError, returnTo, router, source]);

  /**
   * Auth gave up before it could tell us who this is. Starting the Keycloak
   * redirect here would be the worst possible answer: with a live SSO session
   * it returns instantly, fails the same way, and lands back on this page -
   * and when the failure is a provider chunk that never loaded, `login` is a
   * plain page reload, which loops outright.
   */
  if (authError) {
    return <AuthErrorScreen onRetry={() => void startLogin(returnTo)} onLogout={logout} />;
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50">
      <div className="flex flex-col items-center gap-4 text-center">
        <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-blue-600">
          <svg className="h-6 w-6 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 15a4 4 0 004 4h9a5 5 0 10-.1-9.999 5.002 5.002 0 10-9.78 2.096A4.001 4.001 0 003 15z" />
          </svg>
        </div>
        <div className="flex items-center gap-2 text-sm text-gray-500">
          <svg className="h-4 w-4 animate-spin text-blue-600" viewBox="0 0 24 24" fill="none">
            <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
            <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
          </svg>
          Signing you in…
        </div>
      </div>
    </div>
  );
}

function LocalLoginPage() {
  const router = useRouter();
  const { login } = useAuth();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(false);

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    setIsLoading(true);
    try {
      const data = await authApi.login(username, password);
      login(data.token, data.user);
      router.push("/projects");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Login failed. Please try again.");
    } finally {
      setIsLoading(false);
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50">
      <div className="w-full max-w-md">
        <div className="rounded-xl border border-gray-200 bg-white p-8 shadow-sm">
          <div className="mb-8 text-center">
            <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-xl bg-blue-600">
              <svg className="h-6 w-6 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 15a4 4 0 004 4h9a5 5 0 10-.1-9.999 5.002 5.002 0 10-9.78 2.096A4.001 4.001 0 003 15z" />
              </svg>
            </div>
            <h1 className="text-2xl font-bold text-gray-900">DADA Cloud Console</h1>
            <p className="mt-1 text-sm text-gray-500">GitOps-backed self-service infrastructure</p>
          </div>

          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label htmlFor="username" className="block text-sm font-medium text-gray-700">
                Username
              </label>
              <input
                id="username"
                type="text"
                autoComplete="username"
                required
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 placeholder-gray-400 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                placeholder="Enter your username"
              />
            </div>

            <div>
              <label htmlFor="password" className="block text-sm font-medium text-gray-700">
                Password
              </label>
              <input
                id="password"
                type="password"
                autoComplete="current-password"
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 placeholder-gray-400 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                placeholder="Enter your password"
              />
            </div>

            {error && (
              <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
                {error}
              </div>
            )}

            <button
              type="submit"
              disabled={isLoading}
              className="flex w-full items-center justify-center rounded-lg bg-blue-600 px-4 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {isLoading ? (
                <>
                  <svg className="mr-2 h-4 w-4 animate-spin" viewBox="0 0 24 24" fill="none">
                    <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                    <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                  </svg>
                  Signing in...
                </>
              ) : (
                "Sign in"
              )}
            </button>
          </form>
        </div>
      </div>
    </div>
  );
}

export default function LoginPage() {
  if (AUTH_MODE === "oidc") return <OidcLoginPage />;
  return <LocalLoginPage />;
}

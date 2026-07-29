"use client";
import { Suspense, useEffect, useRef } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useAuth } from "@/lib/auth";
import { startRegister } from "@/lib/register-redirect";
import { GOAL_SIGNUP_STARTED, reachGoal, rememberSource } from "@/lib/metrika";

const AUTH_MODE = process.env.NEXT_PUBLIC_AUTH_MODE;

/**
 * Only accepts in-app paths ("/foo") as a returnTo target, rejecting
 * protocol-relative ("//evil.com") or absolute URLs to avoid an open redirect
 * via a query param an attacker fully controls.
 */
function sanitizeReturnTo(value: string | null): string {
  if (!value) return "/projects";
  if (!value.startsWith("/") || value.startsWith("//")) return "/projects";
  return value;
}

/**
 * Sign-up entry point. Sends unauthenticated visitors straight to Keycloak's
 * registration form (via {@link startRegister}) instead of the login form the
 * bare `/login` route lands on. Already-authenticated users are bounced to the
 * console.
 *
 * Accepts an optional `returnTo` (or `next`) query param naming where to land
 * after signup/login — e.g. `/deploy?repo=...` for the one-click deploy flow.
 * Defaults to `/projects`.
 *
 * In non-OIDC (local dev) mode there is no Keycloak registration flow, so this
 * falls back to the login route.
 */
function OidcRegisterPage() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const { isLoading, token } = useAuth();
  const startedRef = useRef(false);
  const returnTo = sanitizeReturnTo(searchParams.get("returnTo") ?? searchParams.get("next"));
  const source = searchParams.get("utm_source") ?? "direct";

  useEffect(() => {
    if (isLoading) return;
    if (token) {
      router.replace(returnTo);
      return;
    }
    if (startedRef.current) return;
    startedRef.current = true;
    rememberSource(source);
    reachGoal(GOAL_SIGNUP_STARTED, { source });
    void startRegister(returnTo);
  }, [isLoading, token, router, returnTo, source]);

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
          Открываем регистрацию…
        </div>
      </div>
    </div>
  );
}

function LocalRegisterRedirect() {
  const router = useRouter();
  useEffect(() => {
    router.replace("/login");
  }, [router]);
  return null;
}

export default function RegisterPage() {
  if (AUTH_MODE === "oidc") {
    return (
      <Suspense fallback={<div className="min-h-screen bg-gray-50" />}>
        <OidcRegisterPage />
      </Suspense>
    );
  }
  return <LocalRegisterRedirect />;
}

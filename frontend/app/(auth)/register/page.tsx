"use client";
import { Suspense, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useAuth } from "@/lib/auth";
import { AuthErrorScreen } from "@/components/shell/auth-error-screen";
import { Button } from "@/components/ui/button";
import { startRegister, type RegisterMethod } from "@/lib/register-redirect";
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
 * Sign-up entry point: a choice screen offering the Yandex broker (one click,
 * no e-mail confirmation) alongside Keycloak's own registration form.
 * Keycloak's registration form renders no IdP buttons of its own, so a
 * visitor who lands on `prompt=create` never sees the Yandex option at all -
 * this page is what makes that path visible before the form takes over.
 *
 * Already-authenticated users are bounced to the console. Accepts an
 * optional `returnTo` (or `next`) query param naming where to land after
 * signup/login - e.g. `/deploy?repo=...` for the one-click deploy flow.
 * Defaults to `/projects`.
 */
function OidcRegisterPage() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const { isLoading, token, authError, logout } = useAuth();
  const [pending, setPending] = useState<RegisterMethod | null>(null);
  const returnTo = sanitizeReturnTo(searchParams.get("returnTo") ?? searchParams.get("next"));
  const source = searchParams.get("utm_source") ?? "direct";

  useEffect(() => {
    if (isLoading || authError) return;
    if (token) router.replace(returnTo);
  }, [isLoading, token, authError, router, returnTo]);

  function handleChoose(method: RegisterMethod) {
    if (pending) return;
    setPending(method);
    rememberSource(source);
    reachGoal(GOAL_SIGNUP_STARTED, { source, method });
    void startRegister(returnTo, method);
  }

  if (authError) {
    return <AuthErrorScreen onRetry={() => window.location.reload()} onLogout={logout} />;
  }

  if (isLoading || token) {
    return <div className="min-h-screen bg-gray-50" />;
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50 px-4">
      <noscript>
        <div className="mb-6 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-center text-sm text-amber-800">
          Для регистрации нужен включенный JavaScript в браузере.
        </div>
      </noscript>
      <div className="w-full max-w-md">
        <div className="rounded-xl border border-gray-200 bg-white p-8 shadow-sm">
          <div className="mb-8 text-center">
            <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-xl bg-blue-600">
              <svg className="h-6 w-6 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 15a4 4 0 004 4h9a5 5 0 10-.1-9.999 5.002 5.002 0 10-9.78 2.096A4.001 4.001 0 003 15z" />
              </svg>
            </div>
            <h1 className="text-2xl font-bold text-gray-900">Создать аккаунт</h1>
            <p className="mt-1 text-sm text-gray-500">Выберите способ регистрации</p>
          </div>

          <div className="space-y-3">
            <Button
              type="button"
              className="w-full"
              size="lg"
              isLoading={pending === "yandex"}
              disabled={pending !== null && pending !== "yandex"}
              onClick={() => handleChoose("yandex")}
            >
              Продолжить с Яндексом
            </Button>
            <Button
              type="button"
              variant="outline"
              className="w-full"
              size="lg"
              isLoading={pending === "email"}
              disabled={pending !== null && pending !== "email"}
              onClick={() => handleChoose("email")}
            >
              Регистрация по e-mail
            </Button>
          </div>

          <p className="mt-6 text-center text-sm text-gray-500">
            Уже есть аккаунт?{" "}
            <Link href="/login" className="font-medium text-blue-600 hover:text-blue-700">
              Войти
            </Link>
          </p>
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

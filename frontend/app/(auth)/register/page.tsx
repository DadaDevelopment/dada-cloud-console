"use client";
import { Suspense, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams, usePathname } from "next/navigation";
import { useAuth } from "@/lib/auth";
import { localeFromPath } from "@/lib/i18n/context";
import { AuthErrorScreen } from "@/components/shell/auth-error-screen";
import { Button } from "@/components/ui/button";
import {
  PENDING_REGISTRATION_KEY,
  isEmailSignupEnabled,
  readAbandonedRegistration,
  startRegister,
  type RegisterMethod,
} from "@/lib/register-redirect";
import { GOAL_SIGNUP_STARTED, reachGoal, rememberSource } from "@/lib/metrika";
import { trackUxEvent } from "@/lib/ux-telemetry";

const AUTH_MODE = process.env.NEXT_PUBLIC_AUTH_MODE;
const EMAIL_SIGNUP_ENABLED = isEmailSignupEnabled(process.env.NEXT_PUBLIC_EMAIL_SIGNUP_ENABLED);

/**
 * Local copy for this page only. Kept out of `lib/i18n/dict.ts` on purpose -
 * that dictionary is for the marketing site's URL-prefix locale, while this
 * page picks its locale from `localeFromPath` the same way but lives in the
 * auth flow and has no other console strings to share a dictionary with.
 */
const REGISTER_COPY = {
  ru: {
    noscript: "Для регистрации нужен включенный JavaScript в браузере.",
    title: "Создать аккаунт",
    subtitle: "Выберите способ регистрации",
    bridgeNotice: "Войдите через Яндекс ID — GitHub подключите на следующем шаге, уже в консоли.",
    abandonedNotice: "Похоже, прошлая попытка регистрации не завершилась.",
    abandonedRetry: "Попробовать еще раз",
    yandex: "Зарегистрироваться через Яндекс ID",
    email: "Регистрация по e-mail",
    emailSignupUnavailable: "Один клик, без пароля и подтверждения почты.",
    haveAccount: "Уже есть аккаунт?",
    signIn: "Войти",
  },
  en: {
    noscript: "Registration requires JavaScript enabled in your browser.",
    title: "Create account",
    subtitle: "Choose how to sign up",
    bridgeNotice: "Sign up with Yandex ID — you'll connect GitHub on the next step, inside the console.",
    abandonedNotice: "Looks like your previous sign-up attempt didn't finish.",
    abandonedRetry: "Try again",
    yandex: "Sign up with Yandex ID",
    email: "Sign up with e-mail",
    emailSignupUnavailable: "One click, no password, no e-mail confirmation.",
    haveAccount: "Already have an account?",
    signIn: "Sign in",
  },
} as const;

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
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const copy = REGISTER_COPY[localeFromPath(pathname)];
  const { isLoading, token, authError, logout } = useAuth();
  const [pending, setPending] = useState<RegisterMethod | null>(null);
  /**
   * The "your last sign-up did not finish" recovery banner state, seeded
   * once from the same {@link PENDING_REGISTRATION_KEY} marker `/callback`
   * clears on every successful auth -- so a value that is still here and
   * past the grace window means a real bounce back from Keycloak, not a
   * person mid-form. Read lazily (not in an effect) so mounting never
   * triggers an extra render.
   */
  const [abandoned, setAbandoned] = useState<{ method: RegisterMethod; ageMs: number } | null>(() => {
    if (typeof window === "undefined") return null;
    try {
      return readAbandonedRegistration(window.localStorage.getItem(PENDING_REGISTRATION_KEY), Date.now());
    } catch {
      return null;
    }
  });
  const abandonedTrackedRef = useRef(false);
  const returnTo = sanitizeReturnTo(searchParams.get("returnTo") ?? searchParams.get("next"));
  const source = searchParams.get("utm_source") ?? "direct";

  useEffect(() => {
    if (isLoading || authError) return;
    if (token) router.replace(returnTo);
  }, [isLoading, token, authError, router, returnTo]);

  /** Fires the abandoned-registration telemetry once per mount, guarded by a ref rather than state. */
  useEffect(() => {
    if (!abandoned || abandonedTrackedRef.current) return;
    abandonedTrackedRef.current = true;
    trackUxEvent("view", "register_abandoned", { method: abandoned.method, age_ms: abandoned.ageMs });
  }, [abandoned]);

  function handleChoose(method: RegisterMethod) {
    if (pending) return;
    setPending(method);
    rememberSource(source);
    reachGoal(GOAL_SIGNUP_STARTED, { source, method });
    void startRegister(returnTo, method);
  }

  function handleRetryAbandoned() {
    if (!abandoned) return;
    try {
      window.localStorage.removeItem(PENDING_REGISTRATION_KEY);
    } catch {}
    setAbandoned(null);
    handleChoose(abandoned.method);
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
          {copy.noscript}
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
            <h1 className="text-2xl font-bold text-gray-900">{copy.title}</h1>
            <p className="mt-1 text-sm text-gray-500">{copy.subtitle}</p>
            <p className="mt-3 text-sm text-gray-600">{copy.bridgeNotice}</p>
          </div>

          {abandoned && (
            <div className="mb-6 rounded-lg border border-blue-200 bg-blue-50 px-4 py-3 text-sm text-blue-800">
              <p>{copy.abandonedNotice}</p>
              <button
                type="button"
                className="mt-2 font-medium text-blue-700 underline hover:text-blue-900"
                onClick={handleRetryAbandoned}
              >
                {copy.abandonedRetry}
              </button>
            </div>
          )}

          <div className="space-y-3">
            <Button
              type="button"
              className="w-full"
              size="lg"
              isLoading={pending === "yandex"}
              disabled={pending !== null && pending !== "yandex"}
              onClick={() => handleChoose("yandex")}
            >
              {copy.yandex}
            </Button>
            {EMAIL_SIGNUP_ENABLED ? (
              <Button
                type="button"
                variant="outline"
                className="w-full"
                size="lg"
                isLoading={pending === "email"}
                disabled={pending !== null && pending !== "email"}
                onClick={() => handleChoose("email")}
              >
                {copy.email}
              </Button>
            ) : (
              <p className="text-center text-sm text-gray-500">{copy.emailSignupUnavailable}</p>
            )}
          </div>

          <p className="mt-6 text-center text-sm text-gray-500">
            {copy.haveAccount}{" "}
            <Link href="/login" className="font-medium text-blue-600 hover:text-blue-700">
              {copy.signIn}
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

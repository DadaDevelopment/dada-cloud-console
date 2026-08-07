"use client";
/**
 * Landing page for the re-activation campaign's promo link.
 *
 * The mailed URL is `/promo/<token>`, one token per recipient, and this page is
 * the only place the campaign funnel is advanced: it records the click, sends
 * the visitor through sign-in when needed, redeems the token for the plan
 * grant, and then hands them to the app templates -- the thing the campaign
 * claims they were missing.
 *
 * Lives at the top level (not under the (console) route group) so it is
 * reachable unauthenticated: the click must be counted even for a recipient
 * who abandons at the login form, otherwise "opened the mail but never came
 * back" is indistinguishable from "never opened it".
 *
 * Sign-in goes through {@link startLogin} rather than the auth context's
 * `login()`, because the context bridge drops the return path and the token
 * would be lost across the Keycloak round-trip.
 */
import { useCallback, useEffect, useRef, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { useAuth } from "@/lib/auth";
import { startLogin } from "@/lib/register-redirect";
import { apiFetch, projectsApi } from "@/lib/api";
import { Spinner } from "@/components/ui/spinner";
import { Button } from "@/components/ui/button";

type RedeemResponse = {
  plan: string;
  days: number;
  granted?: boolean;
  already_redeemed?: boolean;
};

const PLAN_LABELS: Record<string, string> = {
  startup: "Startup",
  business: "Business",
  free: "Free",
};

function planLabel(plan: string): string {
  return PLAN_LABELS[plan] ?? plan;
}

function DadaMark() {
  return (
    <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-blue-600">
      <svg className="h-6 w-6 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth={2}
          d="M3 15a4 4 0 004 4h9a5 5 0 10-.1-9.999 5.002 5.002 0 10-9.78 2.096A4.001 4.001 0 003 15z"
        />
      </svg>
    </div>
  );
}

export default function PromoPage() {
  const params = useParams<{ token: string }>();
  const promoToken = typeof params?.token === "string" ? params.token : "";
  const { isLoading, token } = useAuth();
  const router = useRouter();

  const clickedRef = useRef(false);
  const redeemStartedRef = useRef(false);
  const [result, setResult] = useState<RedeemResponse | null>(null);
  const [target, setTarget] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [wrongAccount, setWrongAccount] = useState(false);

  useEffect(() => {
    if (!promoToken || clickedRef.current) return;
    clickedRef.current = true;
    void apiFetch<void>("/api/v1/promo/click", {
      method: "POST",
      body: { token: promoToken },
      token: "",
    }).catch(() => {});
  }, [promoToken]);

  const redeem = useCallback(async () => {
    try {
      const res = await apiFetch<RedeemResponse>("/api/v1/promo/redeem", {
        method: "POST",
        body: { token: promoToken },
      });
      setResult(res);
      const banner =
        res.granted === false
          ? ""
          : `?promo=${encodeURIComponent(planLabel(res.plan))}&promoDays=${res.days}`;
      try {
        const projects = await projectsApi.list();
        const projectId =
          projects.projects?.[0]?.id ?? (await projectsApi.ensureDefault()).project_id;
        setTarget(`/projects/${projectId}/apps${banner}`);
      } catch {
        setTarget("/projects");
      }
    } catch (err) {
      const status = (err as Error & { status?: number }).status;
      if (status === 403) {
        setWrongAccount(true);
        return;
      }
      if (status === 404) {
        setError("Ссылка не найдена. Возможно, письмо старое — напишите нам, вышлем новую.");
        return;
      }
      setError(err instanceof Error ? err.message : "Не удалось активировать тариф");
    }
  }, [promoToken]);

  useEffect(() => {
    if (!result || !target) return;
    router.replace(target);
  }, [result, target, router]);

  useEffect(() => {
    if (isLoading || !promoToken) return;
    if (!token) {
      void startLogin(`/promo/${promoToken}`);
      return;
    }
    if (redeemStartedRef.current) return;
    redeemStartedRef.current = true;
    void redeem();
  }, [isLoading, token, promoToken, redeem]);

  if (!promoToken || isLoading || !token || (!result && !error && !wrongAccount) || (result && target)) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-gray-50 dark:bg-gray-950">
        <div className="flex flex-col items-center gap-4 text-center">
          <DadaMark />
          <div className="flex items-center gap-2 text-sm text-gray-500">
            <Spinner size="sm" />
            Активируем тариф…
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50 px-4 py-10 dark:bg-gray-950">
      <div className="w-full max-w-md rounded-2xl border border-gray-200 bg-white p-8 shadow-sm dark:border-gray-800 dark:bg-gray-900">
        <div className="flex flex-col items-center gap-3 text-center">
          <DadaMark />
          {result ? (
            <>
              <h1 className="text-xl font-semibold text-gray-900 dark:text-gray-50">
                {result.granted === false
                  ? "У вас уже платный тариф"
                  : `Тариф ${planLabel(result.plan)} на ${result.days} дней активирован`}
              </h1>
              <p className="text-sm text-gray-500 dark:text-gray-400">
                {result.granted === false
                  ? "Ваш текущий тариф не трогали — он лучше промо. Шаблоны доступны так же."
                  : "Карта не нужна: по окончании срока аккаунт сам вернётся на Free, ничего не спишется."}
              </p>
            </>
          ) : wrongAccount ? (
            <>
              <h1 className="text-xl font-semibold text-gray-900 dark:text-gray-50">
                Ссылка выписана на другой аккаунт
              </h1>
              <p className="text-sm text-gray-500 dark:text-gray-400">
                Промо привязано к почте, на которую пришло письмо. Войдите этим аккаунтом — и
                тариф активируется сам.
              </p>
            </>
          ) : (
            <>
              <h1 className="text-xl font-semibold text-gray-900 dark:text-gray-50">
                Не получилось активировать
              </h1>
              <p className="text-sm text-red-700 dark:text-red-300">{error}</p>
            </>
          )}
        </div>

        {result ? (
          <>
            <Link href={target ?? "/projects"}>
              <Button className="mt-6 w-full" size="lg">
                Выбрать шаблон и задеплоить
              </Button>
            </Link>
            <p className="mt-3 text-center text-xs text-gray-500 dark:text-gray-400">
              Готовые репозитории: жмёте «Задеплоить» — через пару минут приложение живёт на своём
              домене с HTTPS.
            </p>
          </>
        ) : wrongAccount ? (
          <Button
            className="mt-6 w-full"
            size="lg"
            variant="outline"
            onClick={() => void startLogin(`/promo/${promoToken}`, true)}
          >
            Войти другим аккаунтом
          </Button>
        ) : (
          <Link href="/projects">
            <Button className="mt-6 w-full" size="lg" variant="outline">
              В консоль
            </Button>
          </Link>
        )}
      </div>
    </div>
  );
}

"use client";

import { Suspense, useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { CheckCircle2, Clock, XCircle } from "lucide-react";
import { billingApi } from "@/lib/api";
import type { Payment } from "@/lib/api";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";
import { checkoutReturnState, resumablePaymentUrl, type CheckoutReturnState } from "@/lib/checkout-status";
import { takeUpgradeIntent, type UpgradeIntent } from "@/lib/upgrade-intent";
import { trackUxEvent } from "@/lib/ux-telemetry";

const POLL_INTERVAL_MS = 3000;

function BillingReturnContent() {
  const { t } = useT();
  const search = useSearchParams();
  const projectId = search.get("project");
  const paymentId = search.get("payment");

  const [payment, setPayment] = useState<Payment | null>(null);
  const [elapsedMs, setElapsedMs] = useState(0);
  const [intent, setIntent] = useState<UpgradeIntent | null>(null);
  const reportedRef = useRef<string | null>(null);

  /**
   * Polls until the row goes terminal. Unlike the old fixed twenty tries, the
   * clock decides what the screen says: the poll keeps running while the screen
   * has already switched to the honest "not completed" state with a way to
   * finish, so a late settlement still lands on the success screen.
   */
  useEffect(() => {
    if (!projectId || !paymentId) return;
    let cancelled = false;
    const startedAt = Date.now();

    async function tick() {
      if (cancelled) return;
      const row = await billingApi
        .payments(projectId as string)
        .then((data) => data.payments.find((p) => p.id === paymentId) ?? null)
        .catch(() => null);
      if (cancelled) return;
      if (row) setPayment(row);
      setElapsedMs(Date.now() - startedAt);
      if (row?.status === "succeeded") {
        setIntent(takeUpgradeIntent());
        return;
      }
      if (row?.status === "canceled") return;
      setTimeout(tick, POLL_INTERVAL_MS);
    }

    void tick();
    return () => {
      cancelled = true;
    };
  }, [projectId, paymentId]);

  const state: CheckoutReturnState =
    projectId && paymentId ? checkoutReturnState(payment, elapsedMs) : "succeeded";
  const resumeUrl = resumablePaymentUrl(payment);

  useEffect(() => {
    if (state === "waiting") return;
    if (reportedRef.current === state) return;
    reportedRef.current = state;
    trackUxEvent("view", `checkout_return:${state}`);
    if (state === "stuck") trackUxEvent("view", "checkout_pending_stuck");
  }, [state]);

  const billingHref = projectId ? `/projects/${projectId}/billing` : "/projects";

  const onResume = useCallback(() => {
    trackUxEvent("click", "payment_resume");
  }, []);

  const onRetryAction = useCallback(() => {
    trackUxEvent("click", "upgrade_dialog:retry_action");
  }, []);

  /** The way back to the thing the customer was buying the plan for. */
  const retryAction =
    state === "succeeded" && intent ? (
      <Link
        href={intent.returnTo}
        onClick={onRetryAction}
        data-ux="checkout_return:retry_action"
        className="mt-6 inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
      >
        {intent.label
          ? `${t("billing.return.retryAction")} — ${intent.label}`
          : t("billing.return.retryAction")}
      </Link>
    ) : null;

  if (state === "waiting") {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-center">
        <Spinner size="lg" />
        <h1 className="mt-4 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("billing.return.waiting")}</h1>
        <p className="mt-2 max-w-md text-sm text-gray-500 dark:text-gray-400">{t("billing.return.waitingHint")}</p>
      </div>
    );
  }

  if (state === "stuck") {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-center">
        <Clock className="h-12 w-12 text-amber-500" />
        <h1 className="mt-4 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("billing.return.stuckTitle")}</h1>
        <p className="mt-2 max-w-md text-sm text-gray-500 dark:text-gray-400">{t("billing.return.stuckMessage")}</p>
        <div className="mt-6 flex flex-wrap items-center justify-center gap-2">
          {resumeUrl && (
            <a
              href={resumeUrl}
              onClick={onResume}
              data-ux="checkout_return:resume"
              className="inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
            >
              {t("billing.return.resume")}
            </a>
          )}
          <Link
            href={billingHref}
            className="inline-flex items-center gap-1.5 rounded-lg border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-50 dark:border-gray-700 dark:text-gray-200 dark:hover:bg-gray-800"
          >
            {t("billing.return.toBilling")}
          </Link>
        </div>
      </div>
    );
  }

  if (state === "canceled") {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-center">
        <XCircle className="h-12 w-12 text-gray-400" />
        <h1 className="mt-4 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("billing.return.canceledTitle")}</h1>
        <p className="mt-2 max-w-md text-sm text-gray-500 dark:text-gray-400">{t("billing.return.canceledMessage")}</p>
        <Link
          href={billingHref}
          className="mt-6 inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
        >
          {t("billing.return.toBilling")}
        </Link>
      </div>
    );
  }

  return (
    <div className="flex flex-col items-center justify-center py-20 text-center">
      <CheckCircle2 className="h-12 w-12 text-green-500" />
      <h1 className="mt-4 text-2xl font-bold text-gray-900 dark:text-gray-100">
        {payment?.status === "succeeded" ? t("billing.return.successTitle") : t("billing.return.title")}
      </h1>
      <p className="mt-2 max-w-md text-sm text-gray-500 dark:text-gray-400">
        {payment?.status === "succeeded" ? t("billing.return.successMessage") : t("billing.return.message")}
      </p>
      {retryAction}
      <Link
        href={billingHref}
        className={
          retryAction
            ? "mt-3 text-sm font-medium text-blue-600 hover:underline dark:text-blue-400"
            : "mt-6 inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
        }
      >
        {projectId ? t("billing.return.toBilling") : t("billing.return.back")}
      </Link>
    </div>
  );
}

export default function BillingReturnPage() {
  return (
    <Suspense
      fallback={
        <div className="flex h-64 items-center justify-center">
          <Spinner size="lg" />
        </div>
      }
    >
      <BillingReturnContent />
    </Suspense>
  );
}

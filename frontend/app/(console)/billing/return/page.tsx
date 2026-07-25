"use client";

import { Suspense, useEffect, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { CheckCircle2, XCircle } from "lucide-react";
import { billingApi } from "@/lib/api";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";

const POLL_INTERVAL_MS = 3000;
const POLL_MAX_TRIES = 20;

type ReturnStatus = "waiting" | "succeeded" | "canceled" | "unknown";

async function pollPaymentStatus(projectId: string, paymentId: string, isCancelled: () => boolean): Promise<ReturnStatus> {
  for (let tries = 0; tries < POLL_MAX_TRIES && !isCancelled(); tries += 1) {
    const status = await billingApi
      .payments(projectId)
      .then((data) => data.payments.find((p) => p.id === paymentId)?.status)
      .catch(() => undefined);
    if (status === "succeeded" || status === "canceled") return status;
    await new Promise((resolve) => setTimeout(resolve, POLL_INTERVAL_MS));
  }
  return "unknown";
}

function BillingReturnContent() {
  const { t } = useT();
  const search = useSearchParams();
  const projectId = search.get("project");
  const paymentId = search.get("payment");

  const [status, setStatus] = useState<ReturnStatus>(projectId && paymentId ? "waiting" : "unknown");

  useEffect(() => {
    if (!projectId || !paymentId) return;
    let cancelled = false;
    pollPaymentStatus(projectId, paymentId, () => cancelled).then((result) => {
      if (!cancelled) setStatus(result);
    });
    return () => { cancelled = true; };
  }, [projectId, paymentId]);

  const billingHref = projectId ? `/projects/${projectId}/billing` : "/projects";

  if (status === "waiting") {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-center">
        <Spinner size="lg" />
        <h1 className="mt-4 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("billing.return.waiting")}</h1>
        <p className="mt-2 max-w-md text-sm text-gray-500 dark:text-gray-400">{t("billing.return.waitingHint")}</p>
      </div>
    );
  }

  if (status === "canceled") {
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
        {status === "succeeded" ? t("billing.return.successTitle") : t("billing.return.title")}
      </h1>
      <p className="mt-2 max-w-md text-sm text-gray-500 dark:text-gray-400">
        {status === "succeeded" ? t("billing.return.successMessage") : t("billing.return.message")}
      </p>
      <Link
        href={billingHref}
        className="mt-6 inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
      >
        {status === "succeeded" || projectId ? t("billing.return.toBilling") : t("billing.return.back")}
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

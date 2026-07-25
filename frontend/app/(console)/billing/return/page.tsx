"use client";

import Link from "next/link";
import { CheckCircle2 } from "lucide-react";
import { useT } from "@/lib/i18n/console/context";

export default function BillingReturnPage() {
  const { t } = useT();

  return (
    <div className="flex flex-col items-center justify-center py-20 text-center">
      <CheckCircle2 className="h-12 w-12 text-green-500" />
      <h1 className="mt-4 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("billing.return.title")}</h1>
      <p className="mt-2 max-w-md text-sm text-gray-500 dark:text-gray-400">{t("billing.return.message")}</p>
      <Link
        href="/projects"
        className="mt-6 inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
      >
        {t("billing.return.back")}
      </Link>
    </div>
  );
}

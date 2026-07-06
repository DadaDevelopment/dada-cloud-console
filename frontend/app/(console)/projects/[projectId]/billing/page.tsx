"use client";

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { billingApi } from "@/lib/api";
import type { BillingAccount, BillingUsage, ConsumptionResponse } from "@/lib/api";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { ConsumptionBreakdown } from "@/components/billing/consumption-breakdown";
import { useT } from "@/lib/i18n/console/context";
import { clsx } from "clsx";

const PLAN_DISPLAY_NAMES: Record<string, string> = {
  free: "Free",
  startup: "Startup",
  business: "Business",
  enterprise: "Enterprise",
};

const PLAN_PRICES_RUB: Record<string, number> = {
  free: 0,
  startup: 990,
  business: 2900,
};

type UsageKey = keyof BillingUsage;

const USAGE_KEYS: UsageKey[] = ["apps", "databases", "storage_gb", "domains", "environments", "members"];

function UsageBar({ used, limit, label }: { used: number; limit: number | null; label: string }) {
  const unlimited = limit === null || limit === 0;
  const pct = unlimited ? 0 : Math.min(100, Math.round((used / limit) * 100));
  const nearLimit = !unlimited && pct >= 80;

  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between text-sm">
        <span className="font-medium text-gray-700 dark:text-gray-200">{label}</span>
        <span className={clsx("text-xs", nearLimit ? "font-semibold text-amber-600 dark:text-amber-400" : "text-gray-400 dark:text-gray-500")}>
          {unlimited ? `${used} / ∞` : `${used} / ${limit}`}
        </span>
      </div>
      {!unlimited && (
        <div className="h-2 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
          <div
            className={clsx(
              "h-full rounded-full transition-all",
              pct >= 90 ? "bg-red-500" : pct >= 80 ? "bg-amber-400" : "bg-blue-500",
            )}
            style={{ width: `${pct}%` }}
          />
        </div>
      )}
    </div>
  );
}

export default function BillingPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const { t } = useT();

  const [account, setAccount] = useState<BillingAccount | null>(null);
  const [consumption, setConsumption] = useState<ConsumptionResponse | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const data = await billingApi.getAccount(projectId);
        if (!cancelled) setAccount(data);
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : t("billing.error.load"));
      } finally {
        if (!cancelled) setIsLoading(false);
      }
    }
    load();
    return () => { cancelled = true; };
  }, [projectId, t]);

  useEffect(() => {
    let cancelled = false;
    billingApi
      .consumption(projectId)
      .then((data) => {
        if (!cancelled) setConsumption(data);
      })
      .catch(() => {
        if (!cancelled) setConsumption(null);
      });
    return () => { cancelled = true; };
  }, [projectId]);

  if (isLoading) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner size="lg" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
        {error}
      </div>
    );
  }

  if (!account) return null;

  const planName = PLAN_DISPLAY_NAMES[account.plan] ?? account.plan;
  const usage = account.usage;

  const nearLimitResources = USAGE_KEYS.filter((k) => {
    const item = usage[k];
    if (!item || item.limit === null || item.limit === 0) return false;
    return item.used / item.limit >= 0.8;
  });

  const quotaLabel = (k: UsageKey): string => t(`billing.quota.${k}`);

  const storageLabel = (v: number) => {
    if (v >= 1024) return `${(v / 1024).toFixed(1)} ТБ`;
    return `${v} ГБ`;
  };

  function formatUsedValue(k: UsageKey, used: number): number | string {
    if (k === "storage_gb") return storageLabel(used);
    return used;
  }

  function formatLimitValue(k: UsageKey, limit: number | null): number | string | null {
    if (limit === null) return null;
    if (k === "storage_gb") return storageLabel(limit);
    return limit;
  }

  return (
    <div>
      <div className="mb-6">
        <Breadcrumb
          items={[
            { label: t("common.crumb.projects"), href: "/projects" },
            { label: planName },
            { label: t("billing.title") },
          ]}
        />
        <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("billing.title")}</h1>
      </div>

      {nearLimitResources.length > 0 && (
        <div className="mb-6 rounded-xl border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-5 py-4">
          <p className="text-sm font-semibold text-amber-800 dark:text-amber-300">{t("billing.alertNearLimit")}</p>
          <ul className="mt-1 space-y-0.5">
            {nearLimitResources.map((k) => {
              const item = usage[k];
              const pct = item.limit ? Math.round((item.used / item.limit) * 100) : 0;
              return (
                <li key={k} className="text-sm text-amber-700 dark:text-amber-400">
                  {t("billing.alertText")
                    .replace("{resource}", quotaLabel(k))
                    .replace("{pct}", String(pct))}
                </li>
              );
            })}
          </ul>
        </div>
      )}

      <div className="grid gap-6 lg:grid-cols-3">
        <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm">
          <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("billing.currentPlan")}</p>
          <p className="mt-2 text-3xl font-bold text-gray-900 dark:text-gray-100">{planName}</p>
          {account.plan !== "enterprise" && PLAN_PRICES_RUB[account.plan] !== undefined && (
            <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {PLAN_PRICES_RUB[account.plan] === 0
                ? "0 ₽"
                : `${PLAN_PRICES_RUB[account.plan].toLocaleString("ru")} ₽ / мес`}
            </p>
          )}
          <Link
            href="https://dada-tuda.ru/pricing"
            target="_blank"
            className="mt-5 inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
          >
            {t("billing.upgradeCta")}
          </Link>
        </div>

        {account.invoicePreview && (
          <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm">
            <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("billing.invoiceTitle")}</p>
            <p className="mt-2 text-3xl font-bold text-gray-900 dark:text-gray-100">
              {account.invoicePreview.amount.toLocaleString("ru")} {t("billing.currency.rub")}
            </p>
            <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{account.invoicePreview.period}</p>
            <span className="mt-4 inline-block rounded-full bg-gray-100 dark:bg-gray-800 px-3 py-1 text-xs font-medium text-gray-500 dark:text-gray-400">
              {t("billing.invoicePreview")}
            </span>
          </div>
        )}

        <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm">
          <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("billing.upgradeTitle")}</p>
          <p className="mt-2 text-sm text-gray-600 dark:text-gray-400">
            {t("billing.alertNearLimit")}
          </p>
          <Link
            href="https://dada-tuda.ru/pricing"
            target="_blank"
            className="mt-5 inline-flex items-center gap-1.5 rounded-lg border border-gray-200 dark:border-gray-700 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 transition-colors hover:bg-gray-50 dark:hover:bg-gray-800"
          >
            {t("billing.upgradeCta")} →
          </Link>
        </div>
      </div>

      <div className="mt-8 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm">
        <h2 className="mb-5 text-sm font-semibold text-gray-900 dark:text-gray-100">{t("billing.usageTitle")}</h2>
        <div className="grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
          {USAGE_KEYS.map((k) => {
            const item = usage[k];
            if (!item) return null;
            const rawLimit = item.limit;
            const displayUsed = formatUsedValue(k, item.used);
            const displayLimit = formatLimitValue(k, rawLimit);
            return (
              <UsageBar
                key={k}
                label={quotaLabel(k)}
                used={typeof displayUsed === "number" ? displayUsed : item.used}
                limit={rawLimit}
              />
            );
          })}
        </div>
        <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {USAGE_KEYS.map((k) => {
            const item = usage[k];
            if (!item) return null;
            const displayUsed = formatUsedValue(k, item.used);
            const displayLimit = formatLimitValue(k, item.limit);
            return (
              <div key={k} className="flex items-center justify-between rounded-lg bg-gray-50 dark:bg-gray-900 px-4 py-3 text-sm">
                <span className="text-gray-500 dark:text-gray-400">{quotaLabel(k)}</span>
                <span className="font-medium text-gray-900 dark:text-gray-100">
                  {displayUsed} / {displayLimit === null ? t("billing.noLimit") : displayLimit}
                </span>
              </div>
            );
          })}
        </div>
      </div>

      {consumption && (
        <div className="mt-8">
          <div className="mb-3 flex items-center justify-between">
            <h2 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("consumption.title")}</h2>
            <span className="text-xs text-gray-400 dark:text-gray-500">{t("consumption.note")}</span>
          </div>
          <ConsumptionBreakdown data={consumption} />
        </div>
      )}
    </div>
  );
}

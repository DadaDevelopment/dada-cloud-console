"use client";

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { billingApi } from "@/lib/api";
import type { BillingAccount, BillingUsage, ConsumptionResponse, BillingPlan, BillingPlanKey, Payment, PaymentStatus } from "@/lib/api";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { ConsumptionBreakdown } from "@/components/billing/consumption-breakdown";
import { useT } from "@/lib/i18n/console/context";
import { clsx } from "clsx";

const PLAN_ORDER: BillingPlanKey[] = ["free", "startup", "business", "enterprise"];

const EXPIRY_SOON_DAYS = 7;

function statusTone(status: PaymentStatus): string {
  if (status === "succeeded") return "bg-green-100 text-green-700 dark:bg-green-950/50 dark:text-green-400";
  if (status === "canceled") return "bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400";
  return "bg-amber-100 text-amber-700 dark:bg-amber-950/50 dark:text-amber-400";
}

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
  const [plans, setPlans] = useState<BillingPlan[]>([]);
  const [payments, setPayments] = useState<Payment[]>([]);
  const [checkoutingPlan, setCheckoutingPlan] = useState<BillingPlanKey | null>(null);
  const [checkoutError, setCheckoutError] = useState<{ plan: BillingPlanKey; message: string } | null>(null);
  const [notConfiguredPlan, setNotConfiguredPlan] = useState<BillingPlanKey | null>(null);
  const [checkoutUrl, setCheckoutUrl] = useState<{ plan: BillingPlanKey; url: string } | null>(null);
  const [loadedAtMs] = useState(() => Date.now());
  const [autopayConsent, setAutopayConsent] = useState(true);
  const [autopayBusy, setAutopayBusy] = useState(false);
  const [autopayError, setAutopayError] = useState<string | null>(null);

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

  useEffect(() => {
    let cancelled = false;
    billingApi
      .getPlans()
      .then((data) => {
        if (!cancelled) setPlans(data.plans);
      })
      .catch(() => {
        if (!cancelled) setPlans([]);
      });
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    let cancelled = false;
    billingApi
      .payments(projectId)
      .then((data) => {
        if (!cancelled) setPayments(data.payments);
      })
      .catch(() => {
        if (!cancelled) setPayments([]);
      });
    return () => { cancelled = true; };
  }, [projectId]);

  async function handleCheckout(plan: BillingPlanKey) {
    setCheckoutError(null);
    setNotConfiguredPlan(null);
    setCheckoutUrl(null);
    setCheckoutingPlan(plan);
    try {
      const resp = await billingApi.checkout(projectId, plan, autopayConsent);
      setCheckoutUrl({ plan, url: resp.confirmation_url });
      window.location.assign(resp.confirmation_url);
    } catch (err) {
      const status = (err as { status?: number } | undefined)?.status;
      if (status === 409) {
        setNotConfiguredPlan(plan);
      } else {
        setCheckoutError({ plan, message: err instanceof Error ? err.message : t("billing.checkoutError") });
      }
    } finally {
      setCheckoutingPlan(null);
    }
  }

  async function handleAutopay(enabled: boolean) {
    setAutopayError(null);
    setAutopayBusy(true);
    try {
      const resp = await billingApi.setAutopay(projectId, enabled);
      setAccount((prev) =>
        prev
          ? {
              ...prev,
              autopay: {
                enabled: resp.autopay_enabled,
                methodTitle: resp.autopay_method_title,
                failures: 0,
                nextChargeAt:
                  resp.autopay_enabled && prev.plan_expires_at
                    ? new Date(new Date(prev.plan_expires_at).getTime() - 24 * 60 * 60 * 1000).toISOString()
                    : null,
              },
            }
          : prev,
      );
    } catch (err) {
      const status = (err as { status?: number } | undefined)?.status;
      setAutopayError(status === 409 ? t("billing.autopayNoMethod") : t("billing.autopayError"));
    } finally {
      setAutopayBusy(false);
    }
  }

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

  const planLabel = (key: string): string => {
    const i18nKey = "billing.plan" + key.charAt(0).toUpperCase() + key.slice(1);
    const label = t(i18nKey);
    return label === i18nKey ? key : label;
  };

  const planName = planLabel(account.plan);
  const usage = account.usage;

  const graceUntil = account.quota_grace_until ? new Date(account.quota_grace_until) : null;
  const graceDate = graceUntil
    ? graceUntil.toLocaleDateString("ru", { day: "numeric", month: "long", year: "numeric" })
    : null;

  const expiresAt = account.plan_expires_at ? new Date(account.plan_expires_at) : null;
  const expiryDaysLeft = expiresAt ? Math.ceil((expiresAt.getTime() - loadedAtMs) / (24 * 60 * 60 * 1000)) : null;
  const expirySoon = expiryDaysLeft !== null && expiryDaysLeft <= EXPIRY_SOON_DAYS;
  const expiryDate = expiresAt
    ? expiresAt.toLocaleDateString("ru", { day: "numeric", month: "long", year: "numeric" })
    : null;

  const quotaEnforced = account.quota_enforced === true;
  const overLimit = account.quota_over_limit ?? [];
  const autopay = account.autopay;
  const autopayNextDate = autopay?.nextChargeAt
    ? new Date(autopay.nextChargeAt).toLocaleDateString("ru", { day: "numeric", month: "long", year: "numeric" })
    : null;

  const nearLimitResources = quotaEnforced
    ? USAGE_KEYS.filter((k) => {
        const item = usage[k];
        if (!item || item.limit === null || item.limit === 0) return false;
        const ratio = item.used / item.limit;
        return ratio >= 0.8 && ratio < 1;
      })
    : [];

  const atLimitResources = quotaEnforced
    ? USAGE_KEYS.filter((k) => {
        const item = usage[k];
        if (!item || item.limit === null || item.limit === 0) return false;
        return item.used / item.limit >= 1;
      })
    : [];

  const quotaLabel = (k: UsageKey): string => t(`billing.quota.${k}`);

  const overLimitLabel = (resource: string): string => {
    const key = resource === "team_members" ? "members" : resource;
    const i18nKey = `billing.quota.${key}`;
    const label = t(i18nKey);
    return label === i18nKey ? resource : label;
  };

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

  const currentPlanInfo = plans.find((p) => p.key === account.plan);
  const currentIdx = PLAN_ORDER.indexOf(account.plan);
  const upgradeCandidates = plans
    .filter((p) => PLAN_ORDER.indexOf(p.key) > currentIdx && p.price_rub !== null && p.price_rub > 0)
    .sort((a, b) => PLAN_ORDER.indexOf(a.key) - PLAN_ORDER.indexOf(b.key));

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

      {graceDate && (
        <div className="mb-6 rounded-xl border border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/40 px-5 py-4">
          <p className="text-sm font-semibold text-blue-800 dark:text-blue-300">{t("billing.graceTitle")}</p>
          <p className="mt-1 text-sm text-blue-700 dark:text-blue-400">{t("billing.graceText", { date: graceDate })}</p>
        </div>
      )}

      {overLimit.length > 0 && (
        <div
          className="mb-6 rounded-xl border-2 border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-950/40 px-5 py-4"
          data-ux="billing_quota_banner:over_limit"
        >
          <p className="text-sm font-bold text-red-800 dark:text-red-300">{t("billing.overLimitTitle")}</p>
          <ul className="mt-1 space-y-0.5">
            {overLimit.map((row) => (
              <li key={row.resource} className="text-sm text-red-700 dark:text-red-400">
                {t("billing.overLimitLine", {
                  resource: overLimitLabel(row.resource),
                  used: String(row.used),
                  limit: String(row.limit),
                })}
              </li>
            ))}
          </ul>
          <p className="mt-2 text-sm text-red-700 dark:text-red-400">
            {graceDate ? t("billing.overLimitGrace", { date: graceDate }) : t("billing.overLimitNoGrace")}
          </p>
          <a
            href="#billing-plans"
            data-ux="billing_quota_banner:over_limit_plans"
            className="mt-3 inline-block text-sm font-semibold text-red-700 dark:text-red-400 hover:underline"
          >
            {t("billing.upgradeCta")}
          </a>
        </div>
      )}

      {nearLimitResources.length > 0 && (
        <div
          className="mb-6 rounded-xl border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-5 py-4"
          data-ux="billing_quota_banner:near_limit"
        >
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

      {atLimitResources.length > 0 && (
        <div
          className="mb-6 rounded-xl border border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/40 px-5 py-4"
          data-ux="billing_quota_banner:at_limit"
        >
          <p className="text-sm font-semibold text-blue-800 dark:text-blue-300">{t("billing.alertAtLimit")}</p>
          <ul className="mt-1 space-y-0.5">
            {atLimitResources.map((k) => {
              const item = usage[k];
              return (
                <li key={k} className="text-sm text-blue-700 dark:text-blue-400">
                  {t("billing.alertAtLimitText")
                    .replace("{resource}", quotaLabel(k))
                    .replace("{used}", String(formatUsedValue(k, item.used)))
                    .replace("{limit}", String(formatLimitValue(k, item.limit) ?? item.limit))}
                </li>
              );
            })}
          </ul>
          <p className="mt-2 text-sm text-blue-700 dark:text-blue-400">{t("billing.alertAtLimitSafe")}</p>
          <a
            href="#billing-plans"
            data-ux="billing_quota_banner:view_plans"
            className="mt-3 inline-block text-sm font-semibold text-blue-700 dark:text-blue-400 hover:underline"
          >
            {t("billing.upgradeCta")}
          </a>
        </div>
      )}

      <div className="grid gap-6 lg:grid-cols-2">
        <div id="billing-plans" className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm">
          <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("billing.currentPlan")}</p>
          <p className="mt-2 text-3xl font-bold text-gray-900 dark:text-gray-100">{planName}</p>
          {account.plan !== "enterprise" && currentPlanInfo?.price_rub !== undefined && currentPlanInfo?.price_rub !== null && (
            <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {currentPlanInfo.price_rub === 0
                ? "0 ₽"
                : `${currentPlanInfo.price_rub.toLocaleString("ru")} ₽ / мес`}
            </p>
          )}
          {expiryDate && (
            <p className={clsx("mt-1 text-sm", expirySoon ? "font-semibold text-amber-600 dark:text-amber-400" : "text-gray-500 dark:text-gray-400")}>
              {t(expirySoon ? "billing.expiresSoon" : "billing.expiresOn", { date: expiryDate })}
            </p>
          )}
          <p className="mt-3 text-xs text-gray-400 dark:text-gray-500">{t("billing.orgScope")}</p>

          {account.plan !== "free" && autopay && (
            <div className="mt-4 rounded-lg border border-gray-100 dark:border-gray-800 bg-gray-50 dark:bg-gray-900/60 px-4 py-3">
              <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
                {t("billing.autopayTitle")}
              </p>
              {autopay.enabled ? (
                <>
                  <p className="mt-1 text-sm font-medium text-gray-900 dark:text-gray-100">
                    {t("billing.autopayOn", { method: autopay.methodTitle })}
                  </p>
                  {autopayNextDate && (
                    <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">
                      {t("billing.autopayNextCharge", { date: autopayNextDate })}
                    </p>
                  )}
                  {autopay.failures > 0 && (
                    <p className="mt-1 text-sm font-medium text-amber-600 dark:text-amber-400">
                      {t("billing.autopayFailures", { count: String(autopay.failures) })}
                    </p>
                  )}
                </>
              ) : (
                <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("billing.autopayOff")}</p>
              )}
              <button
                type="button"
                disabled={autopayBusy}
                onClick={() => handleAutopay(!autopay.enabled)}
                data-ux={autopay.enabled ? "billing_autopay:disable" : "billing_autopay:enable"}
                className="mt-2 text-sm font-semibold text-blue-600 dark:text-blue-400 hover:underline disabled:opacity-60"
              >
                {autopayBusy
                  ? t("billing.autopaySaving")
                  : autopay.enabled
                    ? t("billing.autopayDisable")
                    : t("billing.autopayEnable")}
              </button>
              {autopayError && <p className="mt-1 text-xs text-red-600 dark:text-red-400">{autopayError}</p>}
            </div>
          )}

          {(upgradeCandidates.length > 0 ||
            (expirySoon && currentPlanInfo?.price_rub !== null && (currentPlanInfo?.price_rub ?? 0) > 0)) && (
            <label className="mt-4 flex cursor-pointer items-start gap-2.5">
              <input
                type="checkbox"
                checked={autopayConsent}
                onChange={(e) => setAutopayConsent(e.target.checked)}
                data-ux="billing_checkout:autopay_consent"
                className="mt-0.5 h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500 dark:border-gray-600 dark:bg-gray-800"
              />
              <span>
                <span className="block text-sm font-medium text-gray-700 dark:text-gray-200">
                  {t("billing.autopayConsent")}
                </span>
                <span className="mt-0.5 block text-xs text-gray-400 dark:text-gray-500">
                  {t("billing.autopayConsentHint")}
                </span>
              </span>
            </label>
          )}

          {expirySoon && currentPlanInfo && currentPlanInfo.price_rub !== null && currentPlanInfo.price_rub > 0 && (
            <div className="mt-4">
              <button
                type="button"
                disabled={checkoutingPlan === account.plan}
                onClick={() => handleCheckout(account.plan)}
                className="inline-flex w-full items-center justify-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-blue-700 disabled:opacity-60"
              >
                {checkoutingPlan === account.plan
                  ? t("billing.paying")
                  : t("billing.renew", { price: currentPlanInfo.price_rub.toLocaleString("ru") })}
              </button>
              {notConfiguredPlan === account.plan && (
                <p className="mt-1.5 text-xs text-amber-600 dark:text-amber-400">{t("billing.notConfigured")}</p>
              )}
              {checkoutError?.plan === account.plan && (
                <p className="mt-1.5 text-xs text-red-600 dark:text-red-400">{checkoutError.message}</p>
              )}
            </div>
          )}

          {upgradeCandidates.length > 0 && (
            <div className="mt-5 space-y-2 border-t border-gray-100 dark:border-gray-800 pt-4">
              {upgradeCandidates.map((p) => (
                <div key={p.key}>
                  <button
                    type="button"
                    disabled={checkoutingPlan === p.key}
                    onClick={() => handleCheckout(p.key)}
                    className="inline-flex w-full items-center justify-center gap-1.5 rounded-lg border border-blue-600 px-4 py-2 text-sm font-semibold text-blue-600 transition-colors hover:bg-blue-50 disabled:opacity-60 dark:border-blue-500 dark:text-blue-400 dark:hover:bg-blue-950/40"
                  >
                    {checkoutingPlan === p.key
                      ? t("billing.paying")
                      : t("billing.pay").replace("{price}", (p.price_rub ?? 0).toLocaleString("ru"))}
                  </button>
                  {notConfiguredPlan === p.key && (
                    <p className="mt-1.5 text-xs text-amber-600 dark:text-amber-400">{t("billing.notConfigured")}</p>
                  )}
                  {checkoutError?.plan === p.key && (
                    <p className="mt-1.5 text-xs text-red-600 dark:text-red-400">{checkoutError.message}</p>
                  )}
                  {checkoutUrl?.plan === p.key && (
                    <a
                      href={checkoutUrl.url}
                      className="mt-1.5 inline-block text-xs font-medium text-blue-600 dark:text-blue-400 hover:underline"
                    >
                      {t("billing.openCheckout")}
                    </a>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>

        {account.invoicePreview && (
          <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm">
            <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("billing.invoiceTitle")}</p>
            <p className="mt-2 text-3xl font-bold text-gray-900 dark:text-gray-100">
              {(consumption?.total_rub ?? account.invoicePreview.amount).toLocaleString("ru")} {t("billing.currency.rub")}
            </p>
            <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{account.invoicePreview.period}</p>
            <span className="mt-4 inline-block rounded-full bg-gray-100 dark:bg-gray-800 px-3 py-1 text-xs font-medium text-gray-500 dark:text-gray-400">
              {t("billing.invoicePreview")}
            </span>
          </div>
        )}

      </div>

      <div className="mt-8 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm">
        <h2 className="mb-4 text-sm font-semibold text-gray-900 dark:text-gray-100">{t("billing.paymentsTitle")}</h2>
        {payments.length === 0 ? (
          <p className="text-sm text-gray-400 dark:text-gray-500">{t("billing.paymentsEmpty")}</p>
        ) : (
          <div className="space-y-2">
            {payments.map((p) => (
              <div key={p.id} className="flex items-center justify-between rounded-lg bg-gray-50 dark:bg-gray-900 px-4 py-3 text-sm">
                <div className="flex items-center gap-3">
                  <span className="font-medium text-gray-900 dark:text-gray-100">{planLabel(p.plan)}</span>
                  <span className="text-gray-400 dark:text-gray-500">
                    {p.amount_value.toLocaleString("ru")} {p.currency}
                  </span>
                </div>
                <span className={clsx("rounded-full px-3 py-1 text-xs font-medium", statusTone(p.status))}>
                  {t(`billing.status.${p.status}`)}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="mt-8 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm">
        <h2 className="mb-5 text-sm font-semibold text-gray-900 dark:text-gray-100">{t("billing.usageTitle")}</h2>
        <div className="grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
          {USAGE_KEYS.map((k) => {
            const item = usage[k];
            if (!item) return null;
            const rawLimit = item.limit;
            const displayUsed = formatUsedValue(k, item.used);
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

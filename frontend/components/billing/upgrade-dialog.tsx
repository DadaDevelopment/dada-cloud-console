"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { billingApi } from "@/lib/api";
import type { BillingPlan, BillingQuota } from "@/lib/api";
import { useT } from "@/lib/i18n/console/context";
import { pickTargetPlan, QUOTA_FIELD } from "@/lib/plan-reach";
import { saveUpgradeIntent } from "@/lib/upgrade-intent";
import { trackUxEvent } from "@/lib/ux-telemetry";

/** The quota rows worth comparing side by side, in the order users read them. */
const COMPARE_ROWS: (keyof BillingQuota)[] = ["apps", "databases", "storage_gb"];

/**
 * The upgrade decision, taken where it comes up.
 *
 * It replaces the inline QuotaUpsell banner. The banner rendered under a form
 * the user had already filled in, so the choice competed with the work in
 * front of them and either could be lost; the modal interrupts, states what is
 * missing, what it costs, and gets out of the way. It also carries the two
 * things the banner never did: the size actually requested (a 100 GB install
 * must be offered a plan that holds 100 GB, not merely the next one up), and
 * the intent to come back and finish, which survives the trip to YooKassa.
 *
 * `required` is in the quota's own unit -- GB for storage_gb, a count for the
 * rest.
 */
export function UpgradeDialog({
  resource,
  limit,
  required,
  projectId,
  actionLabel,
  onClose,
}: {
  resource: string;
  limit?: number | null;
  required?: number | null;
  projectId?: string;
  actionLabel?: string;
  onClose: () => void;
}) {
  const { t } = useT();
  const [plans, setPlans] = useState<BillingPlan[] | null>(null);
  const [currentPlan, setCurrentPlan] = useState<BillingPlan | null>(null);
  const [autorenew, setAutorenew] = useState(true);
  const [starting, setStarting] = useState(false);
  const [checkoutError, setCheckoutError] = useState<string | null>(null);
  const viewedRef = useRef<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    billingApi
      .getPlans()
      .then((data) => {
        if (!cancelled) setPlans(data.plans);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!projectId || !plans) return;
    let cancelled = false;
    billingApi
      .getAccount(projectId)
      .then((account) => {
        if (cancelled) return;
        setCurrentPlan(plans.find((p) => p.key === account.plan) ?? null);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [projectId, plans]);

  useEffect(() => {
    if (viewedRef.current === resource) return;
    viewedRef.current = resource;
    trackUxEvent("view", `upgrade_dialog:${resource}`);
  }, [resource]);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const target = pickTargetPlan(plans, resource, { currentLimit: limit ?? null, required: required ?? null });
  const resourceLabel = t(`spend.quota.${resource}`).toLowerCase();
  const price = target?.price_rub ?? 0;

  async function startCheckout() {
    if (!target || !projectId) return;
    setStarting(true);
    setCheckoutError(null);
    trackUxEvent("click", `upgrade_dialog:checkout:${target.key}`);
    saveUpgradeIntent({
      returnTo: window.location.pathname + window.location.search,
      resource,
      plan: target.key,
      label: actionLabel,
    });
    try {
      const res = await billingApi.checkout(projectId, target.key, autorenew);
      window.location.assign(res.confirmation_url);
    } catch (err) {
      setCheckoutError(err instanceof Error ? err.message : t("upgrade.dialog.error"));
      setStarting(false);
    }
  }

  function quotaText(plan: BillingPlan | null, field: keyof BillingQuota): string {
    const value = plan?.quotas?.[field];
    if (value == null) return "—";
    return String(value);
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      role="dialog"
      aria-modal="true"
      aria-label={t("upgrade.dialog.title")}
    >
      <div className="w-full max-w-lg rounded-2xl border border-gray-200 bg-white p-6 shadow-xl dark:border-gray-800 dark:bg-gray-900">
        <h3 className="text-base font-semibold text-gray-900 dark:text-gray-100">{t("upgrade.dialog.title")}</h3>
        <p className="mt-1 text-sm text-gray-600 dark:text-gray-400">
          {required != null && limit != null
            ? t("upgrade.dialog.textRequired", {
                resource: resourceLabel,
                required: String(required),
                limit: String(limit),
              })
            : t("upgrade.dialog.text", { resource: resourceLabel, limit: limit != null ? String(limit) : "—" })}
        </p>

        {target ? (
          <>
            <div className="mt-4 overflow-hidden rounded-xl border border-gray-200 dark:border-gray-800">
              <div className="grid grid-cols-3 gap-2 bg-gray-50 px-3 py-2 text-xs font-medium text-gray-500 dark:bg-gray-800/60 dark:text-gray-400">
                <span />
                <span>{t("upgrade.dialog.compareCurrent", { plan: currentPlan?.name ?? "—" })}</span>
                <span>{t("upgrade.dialog.compareTarget", { plan: target.name })}</span>
              </div>
              {COMPARE_ROWS.filter((field) => field === QUOTA_FIELD[resource] || target.quotas?.[field] != null).map(
                (field) => (
                  <div
                    key={field}
                    className="grid grid-cols-3 gap-2 border-t border-gray-100 px-3 py-2 text-sm dark:border-gray-800"
                  >
                    <span className="text-gray-500 dark:text-gray-400">{t(`spend.quota.${field}`)}</span>
                    <span className="text-gray-700 dark:text-gray-300">{quotaText(currentPlan, field)}</span>
                    <span className="font-semibold text-gray-900 dark:text-gray-100">{quotaText(target, field)}</span>
                  </div>
                ),
              )}
            </div>

            <p className="mt-3 text-sm font-semibold text-gray-900 dark:text-gray-100">
              {t("upgrade.dialog.price", { price: String(price) })}
            </p>

            <label className="mt-3 flex items-start gap-2 text-sm text-gray-700 dark:text-gray-300">
              <input
                type="checkbox"
                checked={autorenew}
                onChange={(e) => setAutorenew(e.target.checked)}
                className="mt-0.5"
              />
              <span>
                {t("upgrade.dialog.autorenew")}
                <span className="mt-0.5 block text-xs text-gray-500 dark:text-gray-400">
                  {t("upgrade.dialog.autorenewHint")}
                </span>
              </span>
            </label>

            <div className="mt-5 flex items-center justify-end gap-2">
              <button
                type="button"
                onClick={onClose}
                data-ux="upgrade_dialog:cancel"
                className="rounded-lg px-3 py-2 text-sm text-gray-600 transition-colors hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-800"
              >
                {t("upgrade.dialog.cancel")}
              </button>
              <button
                type="button"
                onClick={startCheckout}
                disabled={starting || !projectId}
                data-ux="upgrade_dialog:checkout"
                className="inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-blue-700 disabled:opacity-60"
              >
                {starting ? t("upgrade.dialog.starting") : t("upgrade.dialog.pay", { price: String(price) })}
              </button>
            </div>
            {checkoutError && <p className="mt-2 text-sm text-red-700 dark:text-red-300">{checkoutError}</p>}
          </>
        ) : (
          <div className="mt-4">
            <p className="text-sm text-gray-600 dark:text-gray-400">{t("upgrade.dialog.noPlan")}</p>
            <div className="mt-4 flex items-center justify-end gap-2">
              <button
                type="button"
                onClick={onClose}
                data-ux="upgrade_dialog:cancel"
                className="rounded-lg px-3 py-2 text-sm text-gray-600 transition-colors hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-800"
              >
                {t("upgrade.dialog.close")}
              </button>
              <Link
                href="/pricing"
                data-ux="upgrade_dialog:pricing"
                className="inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
              >
                {t("upgrade.dialog.plansCta")}
              </Link>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

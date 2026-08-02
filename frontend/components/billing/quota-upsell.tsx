"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { billingApi } from "@/lib/api";
import type { BillingPlan, BillingQuota } from "@/lib/api";
import { useT } from "@/lib/i18n/console/context";
import { trackUxEvent } from "@/lib/ux-telemetry";

/** Resources the quota gate can refuse, mapped to the plan quota that raises them. */
const QUOTA_FIELD: Record<string, keyof BillingQuota> = {
  apps: "apps",
  databases: "databases",
  domains: "domains",
  team_members: "members",
};

/**
 * The screen a user gets when the plan gate refuses a create.
 *
 * A 403 is where the price finally becomes real, so this is not an error
 * message: it names the cheapest plan that actually raises the limit that was
 * hit, shows what it costs, and starts checkout for it. Sending people to a
 * generic pricing page at this exact moment loses the ones who were ready to
 * pay for the thing they were already trying to do.
 *
 * The plan is chosen from the live catalog rather than hardcoded, so a price
 * change in box-fleet pricing never leaves a stale number in the console. If
 * the catalog cannot be read, or no plan raises this limit (already on the
 * largest), it degrades to the plain pricing link.
 */
export function QuotaUpsell({
  resource,
  limit,
  projectId,
}: {
  resource: string;
  limit?: number;
  projectId?: string;
}) {
  const { t } = useT();
  const [plans, setPlans] = useState<BillingPlan[] | null>(null);
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
    if (viewedRef.current === resource) return;
    viewedRef.current = resource;
    trackUxEvent("view", `quota_upsell:${resource}`);
  }, [resource]);

  const field = QUOTA_FIELD[resource];
  const target = plans
    ?.filter((p) => (p.price_rub ?? 0) > 0)
    .filter((p) => {
      if (!field) return false;
      const q = p.quotas?.[field];
      return q != null && (limit == null || q > limit);
    })
    .sort((a, b) => (a.price_rub ?? 0) - (b.price_rub ?? 0))[0];

  async function startCheckout() {
    if (!target || !projectId) return;
    setStarting(true);
    setCheckoutError(null);
    try {
      const res = await billingApi.checkout(projectId, target.key);
      window.location.assign(res.confirmation_url);
    } catch (err) {
      setCheckoutError(err instanceof Error ? err.message : t("quota.upsell.error"));
      setStarting(false);
    }
  }

  const resourceLabel = t(`spend.quota.${resource}`).toLowerCase();

  return (
    <div role="alert" className="rounded-lg border border-blue-200 bg-blue-50 px-4 py-3 text-sm dark:border-blue-900 dark:bg-blue-950/40">
      <p className="font-semibold text-blue-800 dark:text-blue-300">{t("quota.upsell.title")}</p>
      <p className="mt-1 text-blue-700 dark:text-blue-400">
        {limit != null
          ? t("quota.upsell.text", { resource: resourceLabel, limit: String(limit) })
          : t("quota.upsell.textNoLimit", { resource: resourceLabel })}
      </p>

      {target && projectId ? (
        <>
          <button
            type="button"
            onClick={startCheckout}
            disabled={starting}
            data-ux="quota_upsell:checkout"
            className="mt-3 inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-blue-700 disabled:opacity-60"
          >
            {starting
              ? t("quota.upsell.starting")
              : t("quota.upsell.cta", {
                  plan: target.name,
                  price: String(target.price_rub ?? 0),
                })}
          </button>
          {checkoutError && <p className="mt-2 text-red-700 dark:text-red-300">{checkoutError}</p>}
        </>
      ) : (
        <Link
          href="/pricing"
          data-ux="quota_upsell:pricing"
          className="mt-3 inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
        >
          {t("quota.upsell.plansCta")}
        </Link>
      )}
    </div>
  );
}

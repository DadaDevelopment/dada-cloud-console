"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { billingApi } from "@/lib/api";
import type { AccountSummary } from "@/lib/api";
import { formatRub } from "@/lib/format";
import { useProjectContext } from "@/lib/project-context";
import { useT } from "@/lib/i18n/console/context";

function CoinIcon({ className }: { className?: string }) {
  return (
    <svg className={className} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5} aria-hidden="true">
      <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 18L9 11.25l4.306 4.307a11.95 11.95 0 015.814-5.519l2.74-1.22m0 0l-5.94-2.28m5.94 2.28l-2.28 5.941" />
    </svg>
  );
}

/**
 * Resources shown in the plan meter, in the order a user runs into them. A
 * resource whose limit is 0 (unlimited on that plan) is omitted rather than
 * rendered as "3 of 0".
 */
const QUOTA_ORDER = ["apps", "databases", "domains", "team_members"] as const;

/** Renders the grace deadline as a plain date, no time-of-day noise. */
function formatGraceDate(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString();
}

/**
 * Compact top-bar pill showing the current-month account spend. Clicking opens
 * a dropdown (styled to match the account menu) with plan, month spend, balance
 * and a link to billing. Fetch failures render nothing — the widget is a nicety,
 * not load-bearing chrome, so it must never spam errors into the shell.
 */
export function SpendWidget() {
  const { t } = useT();
  const { projectId } = useProjectContext();
  const [summary, setSummary] = useState<AccountSummary | null>(null);
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let cancelled = false;
    billingApi
      .accountSummary()
      .then((data) => {
        if (!cancelled) setSummary(data);
      })
      .catch(() => {
        /* silent — see component doc */
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!open) return;
    function onClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  if (!summary) return null;

  const monthLabel = t("consumption.perMonth", { amount: formatRub(summary.period_spend_rub) });
  const quotaRows = QUOTA_ORDER.flatMap((key) => {
    const row = summary.quotas?.[key];
    return row && row.limit > 0 ? [{ key, used: row.used, limit: row.limit }] : [];
  });

  return (
    <div ref={ref} className="relative hidden sm:block">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t("spend.label")}
        className="flex h-9 items-center gap-1.5 rounded-lg border border-slate-700 bg-slate-800 px-2.5 text-sm text-slate-200 transition-colors hover:bg-slate-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-400 focus-visible:ring-offset-2 focus-visible:ring-offset-slate-900"
      >
        <CoinIcon className="h-4 w-4 text-slate-400" />
        <span className="tabular-nums font-medium">{monthLabel}</span>
      </button>

      {open && (
        <div role="menu" className="absolute right-0 z-50 mt-2 w-64 overflow-hidden rounded-xl border border-gray-200 bg-white shadow-2xl dark:border-gray-800 dark:bg-gray-900">
          <div className="border-b border-gray-100 px-4 py-3 dark:border-gray-800">
            <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("spend.plan")}</p>
            <p className="mt-0.5 text-sm font-semibold capitalize text-gray-900 dark:text-gray-100">{summary.plan}</p>
          </div>
          <div className="space-y-2 px-4 py-3">
            <div className="flex items-center justify-between text-sm">
              <span className="text-gray-500 dark:text-gray-400">{t("spend.thisMonth")}</span>
              <span className="font-medium tabular-nums text-gray-900 dark:text-gray-100">{formatRub(summary.period_spend_rub)}</span>
            </div>
            <div className="flex items-center justify-between text-sm">
              <span className="text-gray-500 dark:text-gray-400">{t("spend.balance")}</span>
              <span className="font-medium tabular-nums text-gray-900 dark:text-gray-100">{formatRub(summary.balance_rub)}</span>
            </div>
          </div>
          {quotaRows.length > 0 && (
            <div className="border-t border-gray-100 px-4 py-3 dark:border-gray-800">
              <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
                {t("spend.quota.title", { plan: summary.plan })}
              </p>
              <div className="mt-2 space-y-1.5">
                {quotaRows.map((row) => (
                  <div key={row.key} className="flex items-center justify-between text-sm">
                    <span className="text-gray-500 dark:text-gray-400">{t(`spend.quota.${row.key}`)}</span>
                    <span
                      className={`font-medium tabular-nums ${
                        row.used >= row.limit ? "text-amber-600 dark:text-amber-400" : "text-gray-900 dark:text-gray-100"
                      }`}
                    >
                      {t("spend.quota.value", { used: String(row.used), limit: String(row.limit) })}
                    </span>
                  </div>
                ))}
              </div>
              {summary.quota_grace_until && (
                <p className="mt-2 text-xs text-gray-500 dark:text-gray-400">
                  {t("spend.quota.grace", { date: formatGraceDate(summary.quota_grace_until) })}
                </p>
              )}
              <Link
                href="/pricing"
                onClick={() => setOpen(false)}
                className="mt-2 inline-block text-sm font-medium text-blue-600 hover:underline dark:text-blue-400"
              >
                {t("spend.quota.upgrade")} →
              </Link>
            </div>
          )}

          {projectId && (
            <div className="border-t border-gray-100 py-1 dark:border-gray-800">
              <Link
                role="menuitem"
                href={`/projects/${projectId}/billing`}
                onClick={() => setOpen(false)}
                className="block px-4 py-2 text-sm font-medium text-blue-600 hover:bg-gray-50 dark:text-blue-400 dark:hover:bg-gray-800"
              >
                {t("spend.toBilling")} →
              </Link>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

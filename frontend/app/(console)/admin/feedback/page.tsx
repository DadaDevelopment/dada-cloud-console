"use client";
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { adminApi } from "@/lib/api";
import type { FeedbackItem } from "@/lib/types";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { AdminTabs } from "@/components/console/admin-tabs";
import { useT } from "@/lib/i18n/console/context";

const PAGE_SIZE = 100;
const REFRESH_MS = 60_000;
const STATUSES = ["new", "in_progress", "resolved", ""] as const;

const STATUS_BADGE: Record<string, string> = {
  new: "bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-400",
  in_progress: "bg-blue-50 text-blue-700 dark:bg-blue-950/40 dark:text-blue-400",
  resolved: "bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400",
};

const FILTER_LABEL: Record<string, string> = {
  "": "adminFeedback.filter.all",
  new: "adminFeedback.filter.new",
  in_progress: "adminFeedback.filter.inProgress",
  resolved: "adminFeedback.filter.resolved",
};

/**
 * Platform-admin queue for in-product support tickets. Tickets used to land in
 * a table only psql could read; this page plus the operator email is what makes
 * them answerable, and the auto-fix button is what makes the ones about a
 * deployed app fixable without a human reading logs first.
 */
export default function AdminFeedbackPage() {
  const { t } = useT();

  const [items, setItems] = useState<FeedbackItem[]>([]);
  const [newCount, setNewCount] = useState(0);
  const [statusFilter, setStatusFilter] = useState<string>("new");
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [forbidden, setForbidden] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);

  const load = useCallback(async (opts: { silent?: boolean } = {}) => {
    if (!opts.silent) setIsLoading(true);
    setError(null);
    try {
      const data = await adminApi.listFeedback({ status: statusFilter || undefined, limit: PAGE_SIZE });
      setItems(data.items ?? []);
      setNewCount(data.new_count ?? 0);
      setForbidden(false);
    } catch (err) {
      const status = (err as { status?: number } | undefined)?.status;
      if (status === 403) {
        setForbidden(true);
      } else {
        setError(err instanceof Error ? err.message : t("adminFeedback.error.load"));
      }
    } finally {
      setIsLoading(false);
    }
  }, [statusFilter, t]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (forbidden) return;
    const interval = setInterval(() => { void load({ silent: true }); }, REFRESH_MS);
    return () => clearInterval(interval);
  }, [forbidden, load]);

  async function handleAutofix(item: FeedbackItem) {
    setBusyId(item.id);
    setError(null);
    setNotice(null);
    try {
      await adminApi.autofixFeedback(item.id);
      setNotice(t("adminFeedback.autofixStarted"));
      await load({ silent: true });
    } catch (err) {
      setError(err instanceof Error ? err.message : t("adminFeedback.error.action"));
    } finally {
      setBusyId(null);
    }
  }

  async function handleResolve(item: FeedbackItem) {
    const resolution = window.prompt(t("adminFeedback.resolvePrompt"), item.resolution || "");
    if (resolution === null) return;
    setBusyId(item.id);
    setError(null);
    setNotice(null);
    try {
      await adminApi.resolveFeedback(item.id, resolution);
      await load({ silent: true });
    } catch (err) {
      setError(err instanceof Error ? err.message : t("adminFeedback.error.action"));
    } finally {
      setBusyId(null);
    }
  }

  function ageLabel(hours: number): string {
    if (hours >= 48) return t("adminFeedback.age.days", { count: String(Math.floor(hours / 24)) });
    return t("adminFeedback.age.hours", { count: String(Math.max(hours, 0)) });
  }

  const crumbs = [
    { label: t("common.crumb.console"), href: "/projects" },
    { label: t("approvals.crumb.admin") },
    { label: t("adminFeedback.crumb.feedback") },
  ];

  if (forbidden) {
    return (
      <div>
        <Breadcrumb items={crumbs} />
        <div className="mt-4 rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-800 dark:text-amber-300">
          {t("adminFeedback.accessDenied")}
        </div>
      </div>
    );
  }

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div>
          <Breadcrumb items={crumbs} />
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("adminFeedback.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("adminFeedback.subtitle")}</p>
        </div>
        <button
          onClick={() => load()}
          className="inline-flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-200 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
        >
          {t("common.refresh")}
        </button>
      </div>

      <AdminTabs active="feedback" />

      {error && (
        <div className="mb-4 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
      )}
      {notice && (
        <div className="mb-4 rounded-lg border border-emerald-200 dark:border-emerald-900 bg-emerald-50 dark:bg-emerald-950/40 px-4 py-3 text-sm text-emerald-700 dark:text-emerald-400">{notice}</div>
      )}

      <div className="mb-4 flex flex-wrap items-center gap-2">
        {STATUSES.map((s) => (
          <button
            key={s || "all"}
            onClick={() => setStatusFilter(s)}
            className={`rounded-lg px-3 py-1.5 text-sm font-medium transition-colors ${
              statusFilter === s
                ? "bg-blue-600 text-white"
                : "border border-gray-200 dark:border-gray-800 text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800"
            }`}
          >
            {t(FILTER_LABEL[s])}
            {s === "new" && newCount > 0 ? ` (${newCount})` : ""}
          </button>
        ))}
      </div>

      {isLoading ? (
        <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-4 py-10 text-center text-sm text-gray-400">…</div>
      ) : items.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 py-16">
          <p className="text-sm font-medium text-gray-500 dark:text-gray-400">{t("adminFeedback.empty.title")}</p>
          <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("adminFeedback.empty.body")}</p>
        </div>
      ) : (
        <div className="space-y-3">
          {items.map((item) => (
            <div key={item.id} className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4 shadow-sm">
              <div className="mb-2 flex flex-wrap items-center gap-2 text-xs">
                <span className={`rounded px-1.5 py-0.5 font-medium ${STATUS_BADGE[item.status] ?? STATUS_BADGE.resolved}`}>
                  {t(`adminFeedback.status.${item.status}`)}
                </span>
                <span className="font-medium text-gray-700 dark:text-gray-200">{item.email || t("adminFeedback.anonymous")}</span>
                <span className="text-gray-400 dark:text-gray-500">{ageLabel(item.age_hours)}</span>
                {item.app_name && item.project_id ? (
                  <Link
                    href={`/projects/${item.project_id}/apps/${encodeURIComponent(item.app_name)}`}
                    className="font-mono text-blue-600 hover:underline dark:text-blue-400"
                  >
                    {item.app_name}
                  </Link>
                ) : null}
                {item.route ? <span className="font-mono text-gray-400 dark:text-gray-500">{item.route}</span> : null}
              </div>

              <p className="whitespace-pre-wrap text-sm text-gray-900 dark:text-gray-100">{item.message}</p>

              {item.resolution ? (
                <p className="mt-2 text-xs text-gray-500 dark:text-gray-400">{item.resolution}</p>
              ) : null}

              <div className="mt-3 flex flex-wrap items-center gap-2">
                {item.status !== "resolved" && (
                  <button
                    onClick={() => handleAutofix(item)}
                    disabled={!item.autofixable || busyId === item.id}
                    title={item.autofixable ? undefined : t("adminFeedback.notAutofixable")}
                    className="rounded-lg bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-40 transition-colors"
                  >
                    {busyId === item.id ? t("adminFeedback.action.autofixRunning") : t("adminFeedback.action.autofix")}
                  </button>
                )}
                {item.status !== "resolved" && (
                  <button
                    onClick={() => handleResolve(item)}
                    disabled={busyId === item.id}
                    className="rounded-lg border border-gray-200 dark:border-gray-800 px-3 py-1.5 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 disabled:opacity-40 transition-colors"
                  >
                    {t("adminFeedback.action.resolve")}
                  </button>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

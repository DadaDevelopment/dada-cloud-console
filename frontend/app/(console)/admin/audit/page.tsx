"use client";
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { adminApi } from "@/lib/api";
import type { AuditEvent } from "@/lib/types";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { AdminTabs } from "@/components/console/admin-tabs";
import { DataTable, type Column } from "@/components/ui/data-table";
import { useT } from "@/lib/i18n/console/context";

const PAGE_SIZE = 50;
const COHORTS = ["", "customer", "internal", "synthetic", "platform"] as const;

const COHORT_BADGE: Record<string, string> = {
  customer: "bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-400",
  internal: "bg-blue-50 text-blue-700 dark:bg-blue-950/40 dark:text-blue-400",
  synthetic: "bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400",
  platform: "bg-purple-50 text-purple-700 dark:bg-purple-950/40 dark:text-purple-400",
};
const REFRESH_MS = 30_000;

function formatUTC(iso: string): string {
  return new Date(iso).toISOString().replace("T", " ").replace(/\.\d+Z$/, " UTC");
}

function resourceHref(projectId: string, kind: string, name: string): string | null {
  const base = `/projects/${projectId}`;
  switch (kind) {
    case "App":
    case "GitRepo":
      return `${base}/apps/${encodeURIComponent(name)}`;
    case "Build":
      return `${base}/apps/${encodeURIComponent(name)}/builds`;
    case "ServiceDatabase":
      return `${base}/databases/${encodeURIComponent(name)}`;
    case "AppServer":
      return `${base}/app-servers/${encodeURIComponent(name)}`;
    case "S3Bucket":
      return `${base}/storage/${encodeURIComponent(name)}`;
    case "AIModel":
      return `${base}/models/${encodeURIComponent(name)}`;
    default:
      return null;
  }
}

export default function AuditPage() {
  const { t } = useT();

  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [forbidden, setForbidden] = useState(false);

  const [actionFilter, setActionFilter] = useState("");
  const [userFilter, setUserFilter] = useState("");
  const [kindFilter, setKindFilter] = useState("");
  const [appliedAction, setAppliedAction] = useState("");
  const [appliedUser, setAppliedUser] = useState("");
  const [appliedKind, setAppliedKind] = useState("");

  const load = useCallback(async (opts: { silent?: boolean } = {}) => {
    if (!opts.silent) setIsLoading(true);
    setError(null);
    try {
      const data = await adminApi.listAuditEvents({
        action: appliedAction || undefined,
        user: appliedUser || undefined,
        kind: appliedKind || undefined,
        limit: PAGE_SIZE,
        offset,
      });
      setEvents(data.events ?? []);
      setTotal(data.total ?? 0);
      setForbidden(false);
    } catch (err) {
      const status = (err as { status?: number } | undefined)?.status;
      if (status === 403) {
        setForbidden(true);
      } else {
        setError(err instanceof Error ? err.message : t("audit.error.load"));
      }
    } finally {
      setIsLoading(false);
    }
  }, [appliedAction, appliedUser, appliedKind, offset, t]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (forbidden) return;
    const interval = setInterval(() => { void load({ silent: true }); }, REFRESH_MS);
    return () => clearInterval(interval);
  }, [forbidden, load]);

  function applyFilters() {
    setOffset(0);
    setAppliedAction(actionFilter.trim());
    setAppliedUser(userFilter.trim());
    setAppliedKind(kindFilter);
  }

  function clearFilters() {
    setActionFilter("");
    setUserFilter("");
    setKindFilter("");
    setOffset(0);
    setAppliedAction("");
    setAppliedUser("");
    setAppliedKind("");
  }

  const columns: Column<AuditEvent>[] = [
    {
      key: "time",
      header: t("audit.col.time"),
      sortValue: (r) => new Date(r.created_at).getTime(),
      render: (r) => <span className="text-xs text-gray-500 dark:text-gray-400">{formatUTC(r.created_at)}</span>,
    },
    {
      key: "user",
      header: t("audit.col.user"),
      render: (r) => (
        <span className="flex items-center gap-2">
          <span className="text-gray-700 dark:text-gray-200">{r.actor_email}</span>
          {r.account_kind && r.account_kind !== "customer" ? (
            <span className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${COHORT_BADGE[r.account_kind] ?? COHORT_BADGE.synthetic}`}>
              {t(`audit.filter.kind.${r.account_kind}`)}
            </span>
          ) : null}
        </span>
      ),
    },
    { key: "action", header: t("audit.col.action"), render: (r) => <span className="font-mono text-xs text-gray-900 dark:text-gray-100">{r.action}</span> },
    {
      key: "resource",
      header: t("audit.col.resource"),
      render: (r) => {
        const href = r.project_id && r.resource_name ? resourceHref(r.project_id, r.resource_kind, r.resource_name) : null;
        return (
          <span className="text-gray-600 dark:text-gray-400">
            {r.resource_kind ? <span className="mr-1 text-xs text-gray-400 dark:text-gray-500">{r.resource_kind}</span> : null}
            {href ? (
              <Link href={href} className="text-blue-600 hover:underline dark:text-blue-400">{r.resource_name}</Link>
            ) : (
              r.resource_name || "—"
            )}
          </span>
        );
      },
    },
    {
      key: "project",
      header: t("audit.col.project"),
      render: (r) => {
        const label = r.project_name ? `${r.project_name} (${r.project_slug || "—"})` : r.project_slug || "—";
        return r.project_id ? (
          <Link href={`/projects/${r.project_id}`} className="font-mono text-blue-600 hover:underline dark:text-blue-400">{label}</Link>
        ) : (
          <span className="font-mono text-gray-700 dark:text-gray-200">{label}</span>
        );
      },
    },
  ];

  if (forbidden) {
    return (
      <div>
        <Breadcrumb items={[
          { label: t("common.crumb.console"), href: "/projects" },
          { label: t("approvals.crumb.admin") },
          { label: t("audit.crumb.audit") },
        ]} />
        <div className="mt-4 rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-800 dark:text-amber-300">
          {t("audit.accessDenied")}
        </div>
      </div>
    );
  }

  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const page = Math.floor(offset / PAGE_SIZE);

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.console"), href: "/projects" },
              { label: t("approvals.crumb.admin") },
              { label: t("audit.crumb.audit") },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("audit.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("audit.subtitle")}</p>
        </div>
        <button
          onClick={() => load()}
          className="inline-flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-200 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
        >
          <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
          {t("common.refresh")}
        </button>
      </div>

      <AdminTabs active="audit" />

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
      )}

      <div className="mb-4 flex flex-wrap items-center gap-2">
        <input
          value={actionFilter}
          onChange={(e) => setActionFilter(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") applyFilters(); }}
          placeholder={t("audit.filter.actionPlaceholder")}
          className="rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
        <input
          value={userFilter}
          onChange={(e) => setUserFilter(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") applyFilters(); }}
          placeholder={t("audit.filter.userPlaceholder")}
          className="rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
        <select
          value={kindFilter}
          onChange={(e) => setKindFilter(e.target.value)}
          className="rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        >
          {COHORTS.map((k) => (
            <option key={k || "all"} value={k}>{k ? t(`audit.filter.kind.${k}`) : t("audit.filter.kind.all")}</option>
          ))}
        </select>
        <button
          onClick={applyFilters}
          className="rounded-lg bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700 transition-colors"
        >
          {t("audit.filter.apply")}
        </button>
        <button
          onClick={clearFilters}
          className="rounded-lg border border-gray-200 dark:border-gray-800 px-3 py-1.5 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
        >
          {t("audit.filter.clear")}
        </button>
        <span className="ml-auto text-xs text-gray-400 dark:text-gray-500">{t("audit.total").replace("{count}", String(total))}</span>
      </div>

      <DataTable<AuditEvent>
        loading={isLoading}
        rows={events}
        getRowKey={(r) => r.id}
        columns={columns}
        pageSize={PAGE_SIZE}
        emptyState={
          <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 py-16">
            <svg className="mb-3 h-12 w-12 text-gray-300 dark:text-gray-700" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2m-6 9l2 2 4-4" />
            </svg>
            <p className="text-sm font-medium text-gray-500 dark:text-gray-400">{t("audit.empty.title")}</p>
            <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("audit.empty.body")}</p>
          </div>
        }
      />

      {pageCount > 1 && (
        <div className="mt-4 flex items-center justify-end gap-2">
          <button
            disabled={offset === 0}
            onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
            className="rounded-lg border border-gray-200 dark:border-gray-800 px-3 py-1.5 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 disabled:opacity-40 transition-colors"
          >
            {t("audit.pager.prev")}
          </button>
          <span className="text-xs text-gray-400 dark:text-gray-500">{page + 1} / {pageCount}</span>
          <button
            disabled={page + 1 >= pageCount}
            onClick={() => setOffset(offset + PAGE_SIZE)}
            className="rounded-lg border border-gray-200 dark:border-gray-800 px-3 py-1.5 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 disabled:opacity-40 transition-colors"
          >
            {t("audit.pager.next")}
          </button>
        </div>
      )}
    </div>
  );
}

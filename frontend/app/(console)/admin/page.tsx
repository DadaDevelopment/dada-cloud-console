"use client";
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { adminApi } from "@/lib/api";
import type { AdminOverviewResponse } from "@/lib/types";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { DataTable, type Column } from "@/components/ui/data-table";
import { StateChip } from "@/components/ui/state-chip";
import { EChart } from "@/components/charts/echart";
import { useT } from "@/lib/i18n/console/context";
import type { EChartsOption } from "echarts";

const REFRESH_MS = 60_000;
const DYNAMICS_DAYS = 14;

function formatMoney(v: number, currency?: string): string {
  return `${Math.round(v).toLocaleString("ru-RU")} ${currency || "RUB"}`;
}

function phaseTone(phase: string): "error" | "needs-action" | "neutral" {
  if (phase === "Failed" || phase === "Degraded") return "error";
  if (phase === "Pending" || phase === "Unknown" || phase === "Stopped") return "needs-action";
  return "neutral";
}

export default function AdminOverviewPage() {
  const { t } = useT();

  const [data, setData] = useState<AdminOverviewResponse | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [forbidden, setForbidden] = useState(false);

  const load = useCallback(async (opts: { silent?: boolean } = {}) => {
    if (!opts.silent) setIsLoading(true);
    setError(null);
    try {
      const resp = await adminApi.getOverview(DYNAMICS_DAYS);
      setData(resp);
      setForbidden(false);
    } catch (err) {
      const status = (err as { status?: number } | undefined)?.status;
      if (status === 403) {
        setForbidden(true);
      } else {
        setError(err instanceof Error ? err.message : t("adminOverview.error.load"));
      }
    } finally {
      setIsLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (forbidden) return;
    const interval = setInterval(() => { void load({ silent: true }); }, REFRESH_MS);
    return () => clearInterval(interval);
  }, [forbidden, load]);

  const crumb = (
    <Breadcrumb
      items={[
        { label: t("common.crumb.console"), href: "/projects" },
        { label: t("approvals.crumb.admin") },
        { label: t("adminOverview.crumb.overview") },
      ]}
    />
  );

  if (forbidden) {
    return (
      <div>
        {crumb}
        <div className="mt-4 rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-800 dark:text-amber-300">
          {t("adminOverview.accessDenied")}
        </div>
      </div>
    );
  }

  const ready = data?.projects.apps.by_phase["Ready"] ?? 0;
  const appsTotal = data?.projects.apps.total ?? 0;
  const broken = Math.max(0, appsTotal - ready);
  const dates = (data?.dynamics ?? []).map((p) => p.date.slice(5));

  const signupsOption: EChartsOption = {
    xAxis: { type: "category", data: dates },
    yAxis: { type: "value" },
    series: [{
      type: "bar",
      name: t("adminOverview.chart.signups"),
      data: (data?.dynamics ?? []).map((p) => p.signups),
      itemStyle: { color: "#3b82f6", borderRadius: [3, 3, 0, 0] },
    }],
  };

  const buildsOption: EChartsOption = {
    xAxis: { type: "category", data: dates },
    yAxis: { type: "value" },
    series: [
      {
        type: "bar",
        name: t("adminOverview.chart.buildsSuccess"),
        stack: "builds",
        data: (data?.dynamics ?? []).map((p) => p.build_success),
        itemStyle: { color: "#22c55e" },
      },
      {
        type: "bar",
        name: t("adminOverview.chart.buildsFailed"),
        stack: "builds",
        data: (data?.dynamics ?? []).map((p) => p.build_failed),
        itemStyle: { color: "#ef4444" },
      },
    ],
  };

  const newAppsOption: EChartsOption = {
    xAxis: { type: "category", data: dates },
    yAxis: { type: "value" },
    series: [{
      type: "line",
      name: t("adminOverview.chart.newApps"),
      data: (data?.dynamics ?? []).map((p) => p.new_apps),
      smooth: true,
      areaStyle: { opacity: 0.15 },
      itemStyle: { color: "#a855f7" },
    }],
  };

  const notReadyColumns: Column<AdminOverviewResponse["not_ready"][number]>[] = [
    { key: "name", header: t("adminOverview.notReady.col.name"), render: (r) => <span className="font-mono text-xs text-gray-900 dark:text-gray-100">{r.name}</span> },
    { key: "project", header: t("adminOverview.notReady.col.project"), render: (r) => <span className="text-gray-700 dark:text-gray-200">{r.project_name}</span> },
    { key: "phase", header: t("adminOverview.notReady.col.phase"), render: (r) => <StateChip tone={phaseTone(r.phase)}>{r.phase}</StateChip> },
    { key: "owner", header: t("adminOverview.notReady.col.owner"), render: (r) => <span className="text-gray-500 dark:text-gray-400">{r.owner_email || "—"}</span> },
  ];

  return (
    <div>
      <div className="mb-8 flex flex-wrap items-start justify-between gap-3">
        <div>
          {crumb}
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("adminOverview.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("adminOverview.subtitle")}</p>
        </div>
        <div className="flex items-center gap-2">
          <Link
            href="/admin/audit"
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-200 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
          >
            {t("adminOverview.linkAudit")}
          </Link>
          <button
            onClick={() => load()}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-200 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
          >
            {t("common.refresh")}
          </button>
        </div>
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
      )}

      <div className="mb-6 grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Card>
          <CardContent className="p-4">
            <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("adminOverview.kpi.users")}</p>
            <p className="mt-1 text-2xl font-bold text-gray-900 dark:text-gray-100">{isLoading ? "—" : data?.users.total ?? 0}</p>
            <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">
              {t("adminOverview.kpi.usersSub", { d: data?.users.new_24h ?? 0, w: data?.users.new_7d ?? 0, m: data?.users.new_30d ?? 0 })}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("adminOverview.kpi.active48h")}</p>
            <p className="mt-1 text-2xl font-bold text-gray-900 dark:text-gray-100">{isLoading ? "—" : data?.users.active_48h ?? 0}</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("adminOverview.kpi.apps")}</p>
            <p className="mt-1 text-2xl font-bold text-gray-900 dark:text-gray-100">{isLoading ? "—" : appsTotal}</p>
            <p className="mt-1 text-xs">
              <span className={broken > 0 ? "text-red-600 dark:text-red-400" : "text-gray-400 dark:text-gray-500"}>
                {t("adminOverview.kpi.appsSub", { ready, broken })}
              </span>
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("adminOverview.kpi.builds7d")}</p>
            <p className="mt-1 text-2xl font-bold text-gray-900 dark:text-gray-100">
              {isLoading ? "—" : (data?.builds.last_7d_success ?? 0) + (data?.builds.last_7d_failed ?? 0) + (data?.builds.last_7d_canceled ?? 0)}
            </p>
            <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">
              {t("adminOverview.kpi.builds7dSub", { ok: data?.builds.last_7d_success ?? 0, failed: data?.builds.last_7d_failed ?? 0 })}
            </p>
          </CardContent>
        </Card>
      </div>

      <div className="mb-6 grid grid-cols-1 gap-4 md:grid-cols-3">
        <Card>
          <CardHeader className="p-4 pb-2"><CardTitle className="text-sm">{t("adminOverview.chart.signups")}</CardTitle></CardHeader>
          <CardContent className="p-4 pt-0"><EChart option={signupsOption} height={200} /></CardContent>
        </Card>
        <Card>
          <CardHeader className="p-4 pb-2"><CardTitle className="text-sm">{t("adminOverview.chart.builds")}</CardTitle></CardHeader>
          <CardContent className="p-4 pt-0"><EChart option={buildsOption} height={200} /></CardContent>
        </Card>
        <Card>
          <CardHeader className="p-4 pb-2"><CardTitle className="text-sm">{t("adminOverview.chart.newApps")}</CardTitle></CardHeader>
          <CardContent className="p-4 pt-0"><EChart option={newAppsOption} height={200} /></CardContent>
        </Card>
      </div>

      <div className="mb-6 grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-1">
          <CardHeader className="p-4 pb-2"><CardTitle className="text-sm">{t("adminOverview.money.title")}</CardTitle></CardHeader>
          <CardContent className="p-4 pt-0">
            {!isLoading && data && !data.money.available ? (
              <p className="text-sm text-amber-600 dark:text-amber-400">{t("adminOverview.money.unavailable")}</p>
            ) : (
              <>
                <div className="mb-3 grid grid-cols-2 gap-3">
                  <div>
                    <p className="text-xs text-gray-500 dark:text-gray-400">{t("adminOverview.money.total7d")}</p>
                    <p className="text-lg font-semibold text-gray-900 dark:text-gray-100">{data ? formatMoney(data.money.total_7d ?? 0, data.money.currency) : "—"}</p>
                  </div>
                  <div>
                    <p className="text-xs text-gray-500 dark:text-gray-400">{t("adminOverview.money.total30d")}</p>
                    <p className="text-lg font-semibold text-gray-900 dark:text-gray-100">{data ? formatMoney(data.money.total_30d ?? 0, data.money.currency) : "—"}</p>
                  </div>
                </div>
                <p className="mb-2 text-xs font-medium text-gray-500 dark:text-gray-400">{t("adminOverview.money.top")}</p>
                {(data?.money.top ?? []).length === 0 ? (
                  <p className="text-xs text-gray-400 dark:text-gray-500">{t("adminOverview.money.empty")}</p>
                ) : (
                  <ul className="space-y-1.5">
                    {(data?.money.top ?? []).map((p) => (
                      <li key={p.project_id} className="flex items-center justify-between text-sm">
                        <span className="truncate text-gray-700 dark:text-gray-200">{p.project_name}</span>
                        <span className="font-mono text-xs text-gray-500 dark:text-gray-400">{formatMoney(p.cost_30d, data?.money.currency)}</span>
                      </li>
                    ))}
                  </ul>
                )}
              </>
            )}
          </CardContent>
        </Card>

        <Card className="lg:col-span-2">
          <CardHeader className="p-4 pb-2">
            <CardTitle className="text-sm">{t("adminOverview.notReady.title")}</CardTitle>
            <p className="text-xs text-gray-500 dark:text-gray-400">{t("adminOverview.notReady.subtitle")}</p>
          </CardHeader>
          <CardContent className="p-4 pt-0">
            <DataTable
              loading={isLoading}
              rows={data?.not_ready ?? []}
              getRowKey={(r) => `${r.project_name}/${r.name}`}
              columns={notReadyColumns}
              pageSize={10}
              emptyState={
                <div className="flex items-center justify-center rounded-lg border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 py-10">
                  <p className="text-sm font-medium text-green-600 dark:text-green-400">{t("adminOverview.notReady.empty")}</p>
                </div>
              }
            />
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

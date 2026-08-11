"use client";
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { adminApi } from "@/lib/api";
import type { AdminOverviewResponse } from "@/lib/types";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { AdminTabs } from "@/components/console/admin-tabs";
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

/** Compact age label for a raw elapsed-seconds count, e.g. "5 мин назад". */
function ageLabel(seconds: number): string {
  if (seconds < 60) return "только что";
  const mins = Math.floor(seconds / 60);
  if (mins < 60) return `${mins} мин назад`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours} ч назад`;
  const days = Math.floor(hours / 24);
  return `${days} дн назад`;
}

/** Compact duration label for a raw elapsed-seconds count, without "назад" -- used for a lag/gap, not a point in time, e.g. "7 суток". */
function durationLabel(seconds: number): string {
  if (seconds < 60) return "меньше минуты";
  const mins = Math.floor(seconds / 60);
  if (mins < 60) return `${mins} мин`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours} ч`;
  const days = Math.floor(hours / 24);
  return `${days} суток`;
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

  const ready = data?.projects.apps.ready ?? 0;
  const appsTotal = data?.projects.apps.total ?? 0;
  const broken = data?.projects.apps.broken ?? 0;
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

  const notReadyOtherColumns: Column<AdminOverviewResponse["not_ready_other"][number]>[] = [
    { key: "kind", header: "Тип", render: (r) => <span className="text-gray-500 dark:text-gray-400">{r.kind}</span> },
    { key: "name", header: "Имя", render: (r) => <span className="font-mono text-xs text-gray-900 dark:text-gray-100">{r.name}</span> },
    { key: "project", header: t("adminOverview.notReady.col.project"), render: (r) => <span className="text-gray-700 dark:text-gray-200">{r.project_name}</span> },
    {
      key: "phase",
      header: t("adminOverview.notReady.col.phase"),
      render: (r) =>
        r.unmaintained ? (
          <div className="flex flex-col items-start gap-0.5">
            <StateChip tone="protected">Не обслуживается</StateChip>
            <span className="text-[11px] text-gray-400 dark:text-gray-500">статус заморожен на {r.phase}</span>
          </div>
        ) : (
          <StateChip tone={phaseTone(r.phase)}>{r.phase}</StateChip>
        ),
    },
    { key: "age", header: "Как давно", render: (r) => <span className="text-xs text-gray-500 dark:text-gray-400">{ageLabel(r.age_seconds)}</span> },
    {
      key: "lag",
      header: "Отставание сборщика",
      render: (r) =>
        r.unmaintained ? (
          <span className="text-xs font-medium text-slate-600 dark:text-slate-300">{durationLabel(r.kind_lag_seconds)}</span>
        ) : (
          <span className="text-xs text-gray-300 dark:text-gray-600">—</span>
        ),
    },
  ];

  const domainIssueColumns: Column<AdminOverviewResponse["domain_issues"][number]>[] = [
    { key: "hostname", header: "Домен", render: (r) => <span className="font-mono text-xs text-gray-900 dark:text-gray-100">{r.hostname}</span> },
    { key: "stage", header: "Этап", render: (r) => <span className="text-gray-500 dark:text-gray-400">{r.stage === "hostname" ? "Хост" : "Валидация"}</span> },
    { key: "status", header: t("adminOverview.notReady.col.phase"), render: (r) => <StateChip tone={r.status === "failed" ? "error" : "needs-action"}>{r.status}</StateChip> },
    { key: "project", header: t("adminOverview.notReady.col.project"), render: (r) => <span className="text-gray-700 dark:text-gray-200">{r.project_name}</span> },
    { key: "age", header: "Как давно", render: (r) => <span className="text-xs text-gray-500 dark:text-gray-400">{ageLabel(r.age_seconds)}</span> },
  ];

  const stuckOpsColumns: Column<AdminOverviewResponse["stuck_operations"]["oldest"][number]>[] = [
    { key: "action", header: "Действие", render: (r) => <span className="text-gray-900 dark:text-gray-100">{r.action}</span> },
    { key: "resource", header: "Ресурс", render: (r) => <span className="text-gray-700 dark:text-gray-200">{r.resource_kind} {r.resource_name}</span> },
    { key: "status", header: t("adminOverview.notReady.col.phase"), render: (r) => <StateChip tone="needs-action">{r.status}</StateChip> },
    { key: "project", header: t("adminOverview.notReady.col.project"), render: (r) => <span className="text-gray-700 dark:text-gray-200">{r.project_name}</span> },
    { key: "age", header: "Как давно", render: (r) => <span className="text-xs text-gray-500 dark:text-gray-400">{ageLabel(r.age_seconds)}</span> },
  ];

  const failedBuildColumns: Column<AdminOverviewResponse["failed_builds"][number]>[] = [
    { key: "app", header: t("adminOverview.notReady.col.name"), render: (r) => <span className="font-mono text-xs text-gray-900 dark:text-gray-100">{r.app_name}</span> },
    { key: "project", header: t("adminOverview.notReady.col.project"), render: (r) => <span className="text-gray-700 dark:text-gray-200">{r.project_name}</span> },
    { key: "commit", header: "Коммит", render: (r) => <span className="font-mono text-xs text-gray-500 dark:text-gray-400">{(r.commit_sha || "").slice(0, 8) || "—"}</span> },
    { key: "error", header: "Ошибка", render: (r) => <span className="text-xs text-gray-500 dark:text-gray-400">{r.error_message || "—"}</span> },
    { key: "age", header: "Как давно", render: (r) => <span className="text-xs text-gray-500 dark:text-gray-400">{ageLabel(r.age_seconds)}</span> },
  ];

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div>
          {crumb}
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("adminOverview.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("adminOverview.subtitle")}</p>
        </div>
        <button
          onClick={() => load()}
          className="inline-flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-200 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
        >
          {t("common.refresh")}
        </button>
      </div>

      <AdminTabs active="overview" />

      {data && data.not_ready_freshness.blind && (
        <div className="mb-4 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-800 dark:text-red-300">
          <p className="font-medium">Данные устарели, панели нельзя доверять</p>
          <p className="mt-0.5">
            Последний снапшот пришёл {ageLabel(data.not_ready_freshness.newest_sync_age_seconds ?? 0)}.
            {" "}{data.not_ready_freshness.stale_apps} приложений не обновлялись больше 10 минут — сборщик состояния мог упасть или зависнуть.
          </p>
        </div>
      )}

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
                <div className="mb-3 grid grid-cols-3 gap-3">
                  <div>
                    <p className="text-xs text-gray-500 dark:text-gray-400">{t("adminOverview.money.hardware")}</p>
                    <p className="text-lg font-semibold text-gray-900 dark:text-gray-100">{data ? formatMoney(data.money.hardware_total ?? 0, data.money.currency) : "—"}</p>
                  </div>
                  <div>
                    <p className="text-xs text-gray-500 dark:text-gray-400">{t("adminOverview.money.revenue")}</p>
                    <p className="text-lg font-semibold text-gray-900 dark:text-gray-100">{data ? formatMoney(data.money.revenue_total ?? 0, data.money.currency) : "—"}</p>
                  </div>
                  <div>
                    <p className="text-xs text-gray-500 dark:text-gray-400">{t("adminOverview.money.margin")}</p>
                    <p className={`text-lg font-semibold ${(data?.money.margin_total ?? 0) < 0 ? "text-red-600 dark:text-red-400" : "text-gray-900 dark:text-gray-100"}`}>
                      {data ? formatMoney(data.money.margin_total ?? 0, data.money.currency) : "—"}
                    </p>
                  </div>
                </div>
                <div className="mb-3 grid grid-cols-3 gap-3 border-t border-gray-100 dark:border-gray-800/60 pt-3">
                  <div>
                    <p className="text-xs text-gray-500 dark:text-gray-400">{t("adminOverview.money.metered")}</p>
                    <p className="text-lg font-semibold text-gray-900 dark:text-gray-100">{data ? formatMoney(data.money.metered_total ?? 0, data.money.currency) : "—"}</p>
                  </div>
                  <div>
                    <p className="text-xs text-gray-500 dark:text-gray-400">{t("adminOverview.money.paid")}</p>
                    <p className="text-lg font-semibold text-gray-900 dark:text-gray-100">{data ? formatMoney(data.money.paid_total ?? 0, data.money.currency) : "—"}</p>
                  </div>
                  <div>
                    <p className="text-xs text-gray-500 dark:text-gray-400">{t("adminOverview.money.uncollected")}</p>
                    <p className={`text-lg font-semibold ${(data?.money.uncollected_total ?? 0) > 0 ? "text-red-600 dark:text-red-400" : "text-gray-900 dark:text-gray-100"}`}>
                      {data ? formatMoney(data.money.uncollected_total ?? 0, data.money.currency) : "—"}
                    </p>
                  </div>
                </div>
                <p className="mb-2 text-xs font-medium text-gray-500 dark:text-gray-400">{t("adminOverview.money.top")}</p>
                {(data?.money.top_loss_makers ?? []).length === 0 ? (
                  <p className="text-xs text-gray-400 dark:text-gray-500">{t("adminOverview.money.empty")}</p>
                ) : (
                  <ul className="space-y-1.5">
                    {(data?.money.top_loss_makers ?? []).map((c) => (
                      <li key={c.client_name} className="flex items-center justify-between text-sm">
                        <span className="truncate text-gray-700 dark:text-gray-200">{c.client_name}</span>
                        <span className={`font-mono text-xs ${c.margin < 0 ? "text-red-600 dark:text-red-400" : "text-gray-500 dark:text-gray-400"}`}>
                          {formatMoney(c.margin, data?.money.currency)}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
                <Link
                  href="/admin/costs"
                  className="mt-3 inline-block text-xs font-medium text-blue-600 hover:text-blue-700 dark:text-blue-400 dark:hover:text-blue-300"
                >
                  {t("adminOverview.money.fullBreakdown")}
                </Link>
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
                data?.not_ready_freshness.blind ? (
                  <div className="flex items-center justify-center rounded-lg border border-dashed border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-950/20 py-10">
                    <p className="text-sm font-medium text-red-600 dark:text-red-400">Данные устарели — список может быть неполным, пустоту сейчас доверять нельзя</p>
                  </div>
                ) : (
                  <div className="flex items-center justify-center rounded-lg border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 py-10">
                    <p className="text-sm font-medium text-green-600 dark:text-green-400">{t("adminOverview.notReady.empty")}</p>
                  </div>
                )
              }
            />
          </CardContent>
        </Card>
      </div>

      <div className="mb-6 grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader className="p-4 pb-2">
            <CardTitle className="text-sm">Базы, модели и другие ресурсы</CardTitle>
            <p className="text-xs text-gray-500 dark:text-gray-400">
              Ресурсы вне приложений не в статусе Ready: базы данных, ML-модели, прочее. «Не обслуживается» не значит
              «сломано» — по такой строке просто перестали приходить обновления от сборщика статуса.
            </p>
          </CardHeader>
          <CardContent className="p-4 pt-0">
            <DataTable
              loading={isLoading}
              rows={data?.not_ready_other ?? []}
              getRowKey={(r) => `${r.kind}/${r.project_name}/${r.name}`}
              columns={notReadyOtherColumns}
              pageSize={10}
              emptyState={
                <div className="flex items-center justify-center rounded-lg border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 py-10">
                  <p className="text-sm font-medium text-green-600 dark:text-green-400">Все остальные ресурсы в порядке</p>
                </div>
              }
            />
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="p-4 pb-2">
            <CardTitle className="text-sm">Проблемы с доменами</CardTitle>
            <p className="text-xs text-gray-500 dark:text-gray-400">Ошибки выпуска сертификата и залежавшиеся в ожидании домены</p>
          </CardHeader>
          <CardContent className="p-4 pt-0">
            <DataTable
              loading={isLoading}
              rows={data?.domain_issues ?? []}
              getRowKey={(r) => `${r.stage}/${r.project_name}/${r.hostname}`}
              columns={domainIssueColumns}
              pageSize={10}
              emptyState={
                <div className="flex items-center justify-center rounded-lg border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 py-10">
                  <p className="text-sm font-medium text-green-600 dark:text-green-400">С доменами всё в порядке</p>
                </div>
              }
            />
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="p-4 pb-2">
            <CardTitle className="text-sm">Зависшие операции</CardTitle>
            <p className="text-xs text-gray-500 dark:text-gray-400">
              Операции дольше окна перехвата не завершились
              {data ? ` — всего ${data.stuck_operations.count}` : ""}
            </p>
          </CardHeader>
          <CardContent className="p-4 pt-0">
            <DataTable
              loading={isLoading}
              rows={data?.stuck_operations.oldest ?? []}
              getRowKey={(r) => r.id}
              columns={stuckOpsColumns}
              pageSize={10}
              emptyState={
                <div className="flex items-center justify-center rounded-lg border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 py-10">
                  <p className="text-sm font-medium text-green-600 dark:text-green-400">Зависших операций нет</p>
                </div>
              }
            />
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="p-4 pb-2">
            <CardTitle className="text-sm">Последняя сборка упала</CardTitle>
            <p className="text-xs text-gray-500 dark:text-gray-400">Приложение может быть Ready, а последняя сборка при этом сломана</p>
          </CardHeader>
          <CardContent className="p-4 pt-0">
            <DataTable
              loading={isLoading}
              rows={data?.failed_builds ?? []}
              getRowKey={(r) => `${r.project_name}/${r.app_name}`}
              columns={failedBuildColumns}
              pageSize={10}
              emptyState={
                <div className="flex items-center justify-center rounded-lg border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 py-10">
                  <p className="text-sm font-medium text-green-600 dark:text-green-400">Упавших сборок нет</p>
                </div>
              }
            />
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

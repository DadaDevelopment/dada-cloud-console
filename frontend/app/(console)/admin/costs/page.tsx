"use client";
import { useCallback, useEffect, useMemo, useState } from "react";
import { adminApi } from "@/lib/api";
import type { AdminCostClient, AdminCostProject, AdminCostsResponse } from "@/lib/types";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { AdminTabs } from "@/components/console/admin-tabs";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useT } from "@/lib/i18n/console/context";

const REFRESH_MS = 60_000;

function formatMoney(v: number | undefined, currency?: string): string {
  return `${Math.round(v ?? 0).toLocaleString("ru-RU")} ${currency || "RUB"}`;
}

function marginClass(v: number): string {
  if (v > 0) return "text-green-600 dark:text-green-400";
  if (v < 0) return "text-red-600 dark:text-red-400";
  return "text-gray-500 dark:text-gray-400";
}

export default function AdminCostsPage() {
  const { t } = useT();

  const [days, setDays] = useState<7 | 30>(30);
  const [data, setData] = useState<AdminCostsResponse | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [forbidden, setForbidden] = useState(false);
  const [openClients, setOpenClients] = useState<Set<string>>(new Set());
  const [openProjects, setOpenProjects] = useState<Set<string>>(new Set());

  const load = useCallback(async (opts: { silent?: boolean } = {}) => {
    if (!opts.silent) setIsLoading(true);
    setError(null);
    try {
      const resp = await adminApi.getCosts(days);
      setData(resp);
      setForbidden(false);
    } catch (err) {
      const status = (err as { status?: number } | undefined)?.status;
      if (status === 403) {
        setForbidden(true);
      } else {
        setError(err instanceof Error ? err.message : t("adminCosts.error.load"));
      }
    } finally {
      setIsLoading(false);
    }
  }, [days, t]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (forbidden) return;
    const interval = setInterval(() => { void load({ silent: true }); }, REFRESH_MS);
    return () => clearInterval(interval);
  }, [forbidden, load]);

  const toggleClient = (id: string) => {
    setOpenClients((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
  };
  const toggleProject = (key: string) => {
    setOpenProjects((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key); else next.add(key);
      return next;
    });
  };

  const clients = useMemo(() => data?.clients ?? [], [data]);
  const currency = data?.currency;

  const crumb = (
    <Breadcrumb
      items={[
        { label: t("common.crumb.console"), href: "/projects" },
        { label: t("approvals.crumb.admin") },
        { label: t("adminCosts.crumb.costs") },
      ]}
    />
  );

  if (forbidden) {
    return (
      <div>
        {crumb}
        <div className="mt-4 rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-800 dark:text-amber-300">
          {t("adminCosts.accessDenied")}
        </div>
      </div>
    );
  }

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div>
          {crumb}
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("adminCosts.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("adminCosts.subtitle")}</p>
        </div>
        <div className="flex items-center gap-2">
          <div className="flex rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-0.5 shadow-sm">
            {([7, 30] as const).map((d) => (
              <button
                key={d}
                onClick={() => setDays(d)}
                className={`rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
                  days === d
                    ? "bg-blue-600 text-white"
                    : "text-gray-600 hover:text-blue-600 dark:text-gray-300 dark:hover:text-blue-400"
                }`}
              >
                {t(`adminCosts.window.${d}d`)}
              </button>
            ))}
          </div>
          <button
            onClick={() => load()}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-200 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
          >
            {t("common.refresh")}
          </button>
        </div>
      </div>

      <AdminTabs active="costs" />

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
      )}

      {!isLoading && data && !data.available && (
        <div className="mb-6 rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-800 dark:text-amber-300">
          {data.note || t("adminCosts.unavailable")}
        </div>
      )}

      <div className="mb-4 grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Card>
          <CardContent className="p-4">
            <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("adminCosts.kpi.hardware")}</p>
            <p className="mt-1 text-2xl font-bold text-gray-900 dark:text-gray-100">
              {isLoading ? "—" : formatMoney(data?.hardware_total_cost, currency)}
            </p>
            <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">
              {data?.hardware_source ? t(`adminCosts.hardwareSource.${data.hardware_source}`) : ""}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("adminCosts.kpi.revenue")}</p>
            <p className="mt-1 text-2xl font-bold text-gray-900 dark:text-gray-100">
              {isLoading ? "—" : formatMoney(data?.total_revenue, currency)}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("adminCosts.kpi.margin")}</p>
            <p className={`mt-1 text-2xl font-bold ${isLoading ? "text-gray-900 dark:text-gray-100" : marginClass(data?.total_margin ?? 0)}`}>
              {isLoading ? "—" : formatMoney(data?.total_margin, currency)}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("adminCosts.kpi.unallocated")}</p>
            <p className="mt-1 text-2xl font-bold text-gray-900 dark:text-gray-100">
              {isLoading ? "—" : formatMoney(data?.unallocated?.total_cost, currency)}
            </p>
          </CardContent>
        </Card>
      </div>

      {data?.hardware_source === "opencost_only" && (
        <p className="mb-6 text-xs text-gray-400 dark:text-gray-500">{t("adminCosts.hardwareSource.note")}</p>
      )}

      <div className="mb-6 grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-1">
          <CardHeader className="p-4 pb-2"><CardTitle className="text-sm">{t("adminCosts.lossMakers.title")}</CardTitle></CardHeader>
          <CardContent className="p-4 pt-0">
            {(data?.top_loss_makers ?? []).length === 0 ? (
              <p className="text-xs text-gray-400 dark:text-gray-500">{t("adminCosts.lossMakers.empty")}</p>
            ) : (
              <ul className="space-y-1.5">
                {(data?.top_loss_makers ?? []).map((lm) => (
                  <li key={lm.client_name} className="flex items-center justify-between text-sm">
                    <span className="truncate text-gray-700 dark:text-gray-200">{lm.client_name}</span>
                    <span className={`font-mono text-xs ${marginClass(lm.margin)}`}>{formatMoney(lm.margin, currency)}</span>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>

        <Card className="lg:col-span-2">
          <CardHeader className="p-4 pb-2"><CardTitle className="text-sm">{t("adminCosts.table.client")}</CardTitle></CardHeader>
          <CardContent className="p-4 pt-0">
            {isLoading ? (
              <p className="py-6 text-center text-sm text-gray-400 dark:text-gray-500">…</p>
            ) : clients.length === 0 ? (
              <p className="py-6 text-center text-sm text-gray-400 dark:text-gray-500">{t("adminCosts.empty")}</p>
            ) : (
              <CostTree
                clients={clients}
                currency={currency}
                openClients={openClients}
                openProjects={openProjects}
                onToggleClient={toggleClient}
                onToggleProject={toggleProject}
                t={t}
              />
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function CostTree({
  clients,
  currency,
  openClients,
  openProjects,
  onToggleClient,
  onToggleProject,
  t,
}: {
  clients: AdminCostClient[];
  currency?: string;
  openClients: Set<string>;
  openProjects: Set<string>;
  onToggleClient: (id: string) => void;
  onToggleProject: (key: string) => void;
  t: (key: string, vars?: Record<string, string | number>) => string;
}) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-gray-200 dark:border-gray-800 text-xs font-medium text-gray-500 dark:text-gray-400">
            <th className="py-2 text-left">{t("adminCosts.table.client")}</th>
            <th className="py-2 text-right">{t("adminCosts.table.cost")}</th>
            <th className="py-2 text-right">{t("adminCosts.table.revenue")}</th>
            <th className="py-2 text-right">{t("adminCosts.table.margin")}</th>
          </tr>
        </thead>
        <tbody>
          {clients.map((cl) => {
            const clientOpen = openClients.has(cl.client_id);
            return (
              <ClientRows
                key={cl.client_id}
                client={cl}
                currency={currency}
                open={clientOpen}
                onToggle={() => onToggleClient(cl.client_id)}
                openProjects={openProjects}
                onToggleProject={onToggleProject}
              />
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function ClientRows({
  client,
  currency,
  open,
  onToggle,
  openProjects,
  onToggleProject,
}: {
  client: AdminCostClient;
  currency?: string;
  open: boolean;
  onToggle: () => void;
  openProjects: Set<string>;
  onToggleProject: (key: string) => void;
}) {
  return (
    <>
      <tr className="border-b border-gray-100 dark:border-gray-800/60 hover:bg-gray-50 dark:hover:bg-gray-800/40">
        <td className="py-2">
          <button onClick={onToggle} className="flex items-center gap-1.5 font-medium text-gray-900 dark:text-gray-100">
            <span className="inline-block w-3 text-gray-400">{open ? "▾" : "▸"}</span>
            {client.client_name}
          </button>
        </td>
        <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatMoney(client.cost, currency)}</td>
        <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatMoney(client.revenue, currency)}</td>
        <td className={`py-2 text-right font-mono text-xs ${marginClass(client.margin)}`}>{formatMoney(client.margin, currency)}</td>
      </tr>
      {open && client.projects.map((p) => {
        const key = `${client.client_id}/${p.project_id}`;
        return (
          <ProjectRows
            key={key}
            projectKey={key}
            project={p}
            currency={currency}
            open={openProjects.has(key)}
            onToggle={() => onToggleProject(key)}
          />
        );
      })}
    </>
  );
}

function ProjectRows({
  projectKey,
  project,
  currency,
  open,
  onToggle,
}: {
  projectKey: string;
  project: AdminCostProject;
  currency?: string;
  open: boolean;
  onToggle: () => void;
}) {
  return (
    <>
      <tr className="border-b border-gray-100 dark:border-gray-800/60 hover:bg-gray-50 dark:hover:bg-gray-800/40">
        <td className="py-2 pl-6">
          <button onClick={onToggle} className="flex items-center gap-1.5 text-gray-700 dark:text-gray-200">
            <span className="inline-block w-3 text-gray-400">{open ? "▾" : "▸"}</span>
            {project.project_name}
          </button>
        </td>
        <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatMoney(project.cost, currency)}</td>
        <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatMoney(project.revenue, currency)}</td>
        <td className={`py-2 text-right font-mono text-xs ${marginClass(project.margin)}`}>{formatMoney(project.margin, currency)}</td>
      </tr>
      {open && project.resources.map((r) => (
        <tr key={projectKey + "/" + r.name} className="border-b border-gray-50 dark:border-gray-800/30">
          <td className="py-1.5 pl-12 text-xs text-gray-500 dark:text-gray-400">
            {r.name} <span className="text-gray-300 dark:text-gray-600">· {r.kind}</span>
          </td>
          <td className="py-1.5 text-right font-mono text-[11px] text-gray-500 dark:text-gray-400">{formatMoney(r.total_cost, currency)}</td>
          <td className="py-1.5 text-right font-mono text-[11px] text-gray-300 dark:text-gray-600">—</td>
          <td className="py-1.5 text-right font-mono text-[11px] text-gray-300 dark:text-gray-600">—</td>
        </tr>
      ))}
    </>
  );
}

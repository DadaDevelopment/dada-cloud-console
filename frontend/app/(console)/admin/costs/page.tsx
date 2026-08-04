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

function formatPct(v: number | undefined): string {
  return `${Math.round(v ?? 0)}%`;
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

  const toggleClient = (client: AdminCostClient) => {
    const id = client.client_id;
    const wasOpen = openClients.has(id);
    setOpenClients((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
    if (!wasOpen && client.projects.length === 1) {
      const key = `${id}/${client.projects[0].project_id}`;
      setOpenProjects((prev) => new Set(prev).add(key));
    }
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

  const recon = useMemo(() => {
    const clientsSum = (data?.clients ?? []).reduce((s, c) => s + c.cost, 0);
    const platformCost = data?.platform?.cost ?? 0;
    const unalloc = data?.unallocated?.total_cost ?? 0;
    const total = data?.total_cost ?? clientsSum + platformCost + unalloc;
    const hardware = data?.hardware_total_cost ?? 0;
    const delta = total - hardware;
    return { clientsSum, platformCost, unalloc, total, hardware, delta, reconciled: Math.abs(delta) < 1 };
  }, [data]);

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

      <CollectedCard data={data} isLoading={isLoading} t={t} />

      {data?.hardware_source === "opencost_only" && (
        <p className="mb-6 text-xs text-gray-400 dark:text-gray-500">{t("adminCosts.hardwareSource.note")}</p>
      )}

      {(data?.hardware?.length ?? 0) > 0 && (
        <Card className="mb-6">
          <CardHeader className="p-4 pb-2"><CardTitle className="text-sm">{t("adminCosts.hardware.title")}</CardTitle></CardHeader>
          <CardContent className="p-4 pt-0">
            <ul className="grid grid-cols-1 gap-1.5 sm:grid-cols-2 lg:grid-cols-3">
              {(data?.hardware ?? []).map((g) => (
                <li key={`${g.cluster}/${g.name}`} className="flex items-center justify-between rounded-md border border-gray-100 dark:border-gray-800/60 px-3 py-2 text-sm">
                  <span className="truncate text-gray-700 dark:text-gray-200">
                    {g.cluster ? `${g.cluster} / ${g.name}` : g.name}
                    <span className="ml-1.5 text-xs text-gray-400 dark:text-gray-500">
                      {t("adminCosts.hardware.nodeCount", { count: g.node_count })}
                    </span>
                  </span>
                  <span className="font-mono text-xs text-gray-700 dark:text-gray-200">{formatMoney(g.price_month_rub, currency)}</span>
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      )}

      {!isLoading && data?.available && (
        <Card className="mb-6">
          <CardHeader className="p-4 pb-2"><CardTitle className="text-sm">{t("adminCosts.method.title")}</CardTitle></CardHeader>
          <CardContent className="p-4 pt-0">
            <p className="mb-3 text-xs text-gray-500 dark:text-gray-400">{t("adminCosts.method.model")}</p>
            <div className="space-y-1.5 text-sm sm:max-w-md">
              <div className="flex items-center justify-between">
                <span className="text-gray-600 dark:text-gray-300">{t("adminCosts.method.clientsSum")}</span>
                <span className="font-mono text-xs text-gray-700 dark:text-gray-200">{formatMoney(recon.clientsSum, currency)}</span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-gray-600 dark:text-gray-300">+ {t("adminCosts.method.platform")}</span>
                <span className="font-mono text-xs text-gray-700 dark:text-gray-200">{formatMoney(recon.platformCost, currency)}</span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-gray-600 dark:text-gray-300">+ {t("adminCosts.method.unallocated")}</span>
                <span className="font-mono text-xs text-gray-700 dark:text-gray-200">{formatMoney(recon.unalloc, currency)}</span>
              </div>
              <div className="flex items-center justify-between border-t border-gray-200 dark:border-gray-800 pt-1.5 font-medium">
                <span className="text-gray-900 dark:text-gray-100">= {t("adminCosts.method.totalExpenses")}</span>
                <span className="font-mono text-xs text-gray-900 dark:text-gray-100">{formatMoney(recon.total, currency)}</span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-gray-600 dark:text-gray-300">{t("adminCosts.method.hardware")}</span>
                <span className="font-mono text-xs text-gray-700 dark:text-gray-200">{formatMoney(recon.hardware, currency)}</span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-gray-600 dark:text-gray-300">{t("adminCosts.method.delta")}</span>
                <span className={`font-mono text-xs ${recon.reconciled ? "text-green-600 dark:text-green-400" : "text-amber-600 dark:text-amber-400"}`}>
                  {formatMoney(recon.delta, currency)}{recon.reconciled ? ` · ${t("adminCosts.method.reconciled")}` : ""}
                </span>
              </div>
            </div>
            <p className="mt-3 text-xs text-gray-400 dark:text-gray-500">
              {t("adminCosts.method.params", {
                raw: Math.round(data?.opencost_raw_total ?? 0).toLocaleString("ru-RU"),
                scale: (data?.scale_factor ?? 0).toFixed(2),
              })}
            </p>
          </CardContent>
        </Card>
      )}

      {data?.agent_tokens?.available && (
        <Card className="mb-6">
          <CardHeader className="p-4 pb-2"><CardTitle className="text-sm">{t("adminCosts.agent.title")}</CardTitle></CardHeader>
          <CardContent className="p-4 pt-0">
            <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
              <div>
                <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("adminCosts.agent.revenue")}</p>
                <p className="mt-1 text-xl font-bold text-gray-900 dark:text-gray-100">{formatMoney(data.agent_tokens.revenue_rub, currency)}</p>
              </div>
              <div>
                <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("adminCosts.agent.cost")}</p>
                <p className="mt-1 text-xl font-bold text-gray-900 dark:text-gray-100">{formatMoney(data.agent_tokens.cost_rub, currency)}</p>
              </div>
              <div>
                <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("adminCosts.agent.margin")}</p>
                <p className={`mt-1 text-xl font-bold ${marginClass(data.agent_tokens.margin_rub ?? 0)}`}>{formatMoney(data.agent_tokens.margin_rub, currency)}</p>
              </div>
              <div>
                <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("adminCosts.agent.tokens")}</p>
                <p className="mt-1 text-xl font-bold text-gray-900 dark:text-gray-100">{(data.agent_tokens.tokens ?? 0).toLocaleString("ru-RU")}</p>
              </div>
            </div>
            <p className="mt-3 text-xs text-gray-400 dark:text-gray-500">
              {t("adminCosts.agent.params", {
                usd: (data.agent_tokens.cost_usd ?? 0).toFixed(4),
                rate: (data.agent_tokens.usd_rub ?? 0).toFixed(0),
                markup: (data.agent_tokens.markup ?? 0).toFixed(2),
                days: data.agent_tokens.window_days ?? days,
              })}
            </p>
          </CardContent>
        </Card>
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

      {!isLoading && data?.available && data.platform && (data.platform.projects?.length ?? 0) > 0 && (
        <Card className="mb-6">
          <CardHeader className="p-4 pb-2">
            <CardTitle className="text-sm">{t("adminCosts.platform.title")}</CardTitle>
            <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{t("adminCosts.platform.subtitle")}</p>
          </CardHeader>
          <CardContent className="p-4 pt-0">
            <CostTree
              clients={[data.platform]}
              currency={currency}
              openClients={openClients}
              openProjects={openProjects}
              onToggleClient={toggleClient}
              onToggleProject={toggleProject}
              t={t}
              costOnly
            />
          </CardContent>
        </Card>
      )}
    </div>
  );
}

/**
 * CollectedCard puts the platform's two money numbers side by side: what the
 * hourly app-usage ledger says clients consumed, and what actually settled as
 * payments in the same window.
 *
 * Every other money column on this page is modelled — the pricing formula
 * applied to observed usage, computed for a free-plan account exactly as for a
 * paying one — so the page could show a healthy "revenue" while the platform
 * collected nothing. This card is the correction: consumption, money, and the
 * gap between them, per client.
 */
function CollectedCard({
  data,
  isLoading,
  t,
}: {
  data: AdminCostsResponse | null;
  isLoading: boolean;
  t: (key: string, vars?: Record<string, string | number>) => string;
}) {
  const currency = data?.currency;
  const rows = useMemo(() => {
    return (data?.clients ?? [])
      .map((c) => ({
        id: c.client_id,
        name: c.client_name,
        plan: c.plan || "free",
        metered: c.metered_rub ?? 0,
        paid: c.paid_rub ?? 0,
        uncollected: c.uncollected_rub ?? 0,
      }))
      .filter((r) => r.metered > 0 || r.paid > 0)
      .sort((a, b) => b.uncollected - a.uncollected);
  }, [data]);

  if (isLoading || !data?.available) return null;

  const hours = data.ledger_hours ?? 0;
  const since = data.metered_since ? new Date(data.metered_since).toLocaleString("ru-RU") : "";
  const reconstructed = data.reconstructed_rub ?? 0;
  const reconstructedFrom = data.reconstructed_from ? new Date(data.reconstructed_from).toLocaleString("ru-RU") : "";

  return (
    <Card className="mb-6">
      <CardHeader className="p-4 pb-2">
        <CardTitle className="text-sm">{t("adminCosts.collected.title")}</CardTitle>
        <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{t("adminCosts.collected.subtitle")}</p>
      </CardHeader>
      <CardContent className="p-4 pt-0">
        <div className="mb-4 grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div>
            <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("adminCosts.collected.metered")}</p>
            <p className="mt-1 text-2xl font-bold text-gray-900 dark:text-gray-100">{formatMoney(data.total_metered, currency)}</p>
          </div>
          <div>
            <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("adminCosts.collected.paid")}</p>
            <p className="mt-1 text-2xl font-bold text-gray-900 dark:text-gray-100">{formatMoney(data.total_paid, currency)}</p>
          </div>
          <div>
            <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("adminCosts.collected.uncollected")}</p>
            <p className={`mt-1 text-2xl font-bold ${marginClass(-(data.total_uncollected ?? 0))}`}>
              {formatMoney(data.total_uncollected, currency)}
            </p>
          </div>
        </div>

        {rows.length === 0 ? (
          <p className="py-4 text-center text-sm text-gray-400 dark:text-gray-500">{t("adminCosts.collected.empty")}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-gray-200 dark:border-gray-800 text-xs font-medium text-gray-500 dark:text-gray-400">
                  <th className="py-2 text-left">{t("adminCosts.collected.client")}</th>
                  <th className="py-2 text-left">{t("adminCosts.collected.plan")}</th>
                  <th className="py-2 text-right">{t("adminCosts.collected.metered")}</th>
                  <th className="py-2 text-right">{t("adminCosts.collected.paid")}</th>
                  <th className="py-2 text-right">{t("adminCosts.collected.uncollected")}</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => (
                  <tr key={r.id} className="border-b border-gray-100 dark:border-gray-800/60">
                    <td className="py-2 text-left text-gray-900 dark:text-gray-100">{r.name}</td>
                    <td className="py-2 text-left">
                      <span className="rounded-md bg-gray-100 dark:bg-gray-800 px-1.5 py-0.5 font-mono text-[11px] text-gray-600 dark:text-gray-300">
                        {r.plan}
                      </span>
                    </td>
                    <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatMoney(r.metered, currency)}</td>
                    <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatMoney(r.paid, currency)}</td>
                    <td className={`py-2 text-right font-mono text-xs ${marginClass(-r.uncollected)}`}>{formatMoney(r.uncollected, currency)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        <p className="mt-3 text-xs text-gray-400 dark:text-gray-500">
          {hours > 0
            ? t("adminCosts.collected.coverage", { hours, since })
            : t("adminCosts.collected.noLedger")}
        </p>
        {reconstructed > 0 && (
          <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">
            {t("adminCosts.collected.reconstructed", {
              amount: formatMoney(reconstructed, currency),
              since: reconstructedFrom,
            })}
          </p>
        )}
      </CardContent>
    </Card>
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
  costOnly = false,
}: {
  clients: AdminCostClient[];
  currency?: string;
  openClients: Set<string>;
  openProjects: Set<string>;
  onToggleClient: (client: AdminCostClient) => void;
  onToggleProject: (key: string) => void;
  t: (key: string, vars?: Record<string, string | number>) => string;
  costOnly?: boolean;
}) {
  const totalCost = clients.reduce((s, c) => s + c.cost, 0);
  const totalRevenue = clients.reduce((s, c) => s + c.revenue, 0);
  const totalMargin = clients.reduce((s, c) => s + c.margin, 0);
  const totalMarginPct = totalRevenue > 0 ? (totalMargin / totalRevenue) * 100 : 0;
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-gray-200 dark:border-gray-800 text-xs font-medium text-gray-500 dark:text-gray-400">
            <th className="py-2 text-left">{t(costOnly ? "adminCosts.table.node" : "adminCosts.table.client")}</th>
            <th className="py-2 text-right">{t("adminCosts.table.cost")}</th>
            {!costOnly && <th className="py-2 text-right">{t("adminCosts.table.revenue")}</th>}
            {!costOnly && <th className="py-2 text-right">{t("adminCosts.table.marginPct")}</th>}
            {!costOnly && <th className="py-2 text-right">{t("adminCosts.table.margin")}</th>}
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
                onToggle={() => onToggleClient(cl)}
                openProjects={openProjects}
                onToggleProject={onToggleProject}
                costOnly={costOnly}
                t={t}
              />
            );
          })}
        </tbody>
        <tfoot>
          <tr className="border-t-2 border-gray-200 dark:border-gray-700 font-medium">
            <td className="py-2 text-left text-gray-900 dark:text-gray-100">{t("adminCosts.table.total")}</td>
            <td className="py-2 text-right font-mono text-xs text-gray-900 dark:text-gray-100">{formatMoney(totalCost, currency)}</td>
            {!costOnly && <td className="py-2 text-right font-mono text-xs text-gray-900 dark:text-gray-100">{formatMoney(totalRevenue, currency)}</td>}
            {!costOnly && <td className={`py-2 text-right font-mono text-xs ${marginClass(totalMarginPct)}`}>{formatPct(totalMarginPct)}</td>}
            {!costOnly && <td className={`py-2 text-right font-mono text-xs ${marginClass(totalMargin)}`}>{formatMoney(totalMargin, currency)}</td>}
          </tr>
        </tfoot>
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
  costOnly = false,
  t,
}: {
  client: AdminCostClient;
  currency?: string;
  open: boolean;
  onToggle: () => void;
  openProjects: Set<string>;
  onToggleProject: (key: string) => void;
  costOnly?: boolean;
  t: (key: string, vars?: Record<string, string | number>) => string;
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
        {!costOnly && <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatMoney(client.revenue, currency)}</td>}
        {!costOnly && <td className={`py-2 text-right font-mono text-xs ${marginClass(client.margin)}`}>{formatPct(client.margin_pct)}</td>}
        {!costOnly && <td className={`py-2 text-right font-mono text-xs ${marginClass(client.margin)}`}>{formatMoney(client.margin, currency)}</td>}
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
            costOnly={costOnly}
            t={t}
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
  costOnly = false,
  t,
}: {
  projectKey: string;
  project: AdminCostProject;
  currency?: string;
  open: boolean;
  onToggle: () => void;
  costOnly?: boolean;
  t: (key: string, vars?: Record<string, string | number>) => string;
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
        {!costOnly && <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatMoney(project.revenue, currency)}</td>}
        {!costOnly && <td className={`py-2 text-right font-mono text-xs ${marginClass(project.margin)}`}>{formatPct(project.margin_pct)}</td>}
        {!costOnly && <td className={`py-2 text-right font-mono text-xs ${marginClass(project.margin)}`}>{formatMoney(project.margin, currency)}</td>}
      </tr>
      {open && project.resources.map((r) => (
        <tr key={projectKey + "/" + r.name} className="border-b border-gray-50 dark:border-gray-800/30">
          <td className="py-1.5 pl-12 text-xs text-gray-500 dark:text-gray-400">
            {r.kind === "agent" ? (
              t("adminCosts.agentRow.label")
            ) : (
              <>
                {r.name} <span className="text-gray-300 dark:text-gray-600">· {r.kind}</span>
              </>
            )}
          </td>
          <td className="py-1.5 text-right font-mono text-[11px] text-gray-500 dark:text-gray-400">{formatMoney(r.total_cost, currency)}</td>
          {!costOnly && <td className="py-1.5 text-right font-mono text-[11px] text-gray-500 dark:text-gray-400">{formatMoney(r.revenue, currency)}</td>}
          {!costOnly && <td className={`py-1.5 text-right font-mono text-[11px] ${marginClass(r.margin)}`}>{formatPct(r.margin_pct)}</td>}
          {!costOnly && <td className={`py-1.5 text-right font-mono text-[11px] ${marginClass(r.margin)}`}>{formatMoney(r.margin, currency)}</td>}
        </tr>
      ))}
    </>
  );
}

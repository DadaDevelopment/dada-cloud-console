"use client";

import { useCallback, useEffect, useState } from "react";
import { adminApi } from "@/lib/api";
import type { AdminFunnelResponse, AdminFunnelResource } from "@/lib/types";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { AdminTabs } from "@/components/console/admin-tabs";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";
import { ChannelFunnelSankey } from "@/components/console/channel-funnel-sankey";
import { FunnelStageRail, type FunnelStageRailItem } from "@/components/console/funnel-stage-rail";
import type { ChannelFunnelSeries } from "@/lib/channel-funnel";

const WINDOWS = ["7d", "30d", "90d", "all"] as const;

function trafficSourceLabel(source: string): string {
  const labels: Record<string, string> = {
    "Direct traffic": "Прямой", "Internal traffic": "Внутренний", "Search engine traffic": "Поиск", "Link traffic": "Ссылки", "Messenger traffic": "Мессенджеры", "Recommendation system traffic": "Рекомендации",
  };
  return labels[source] ?? source;
}

function rate(previous: number, current: number): number | undefined {
  return previous > 0 && current <= previous ? (current / previous) * 100 : undefined;
}

function resourceLabel(resource: AdminFunnelResource, t: (key: string) => string): string {
  return t(`adminFunnel.resource.${resource.key}`);
}

export default function AdminFunnelPage() {
  const { t } = useT();
  const [windowKey, setWindowKey] = useState<string>("30d");
  const [data, setData] = useState<AdminFunnelResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [forbidden, setForbidden] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await adminApi.getFunnel({ window: windowKey });
      setData(result);
      setForbidden(false);
    } catch (err) {
      const status = (err as { status?: number } | undefined)?.status;
      if (status === 403) setForbidden(true);
      else setError(err instanceof Error ? err.message : t("adminFunnel.error.load"));
    } finally {
      setLoading(false);
    }
  }, [t, windowKey]);

  useEffect(() => {
    void load();
  }, [load]);

  const traffic = data?.channel_funnel;
  const trafficSeries: ChannelFunnelSeries[] = (traffic?.channels ?? []).map((channel) => ({ source: channel.source, values: [channel.users, channel.register_opened, channel.signup_started, channel.registration_complete] }));
  const acquisition = data?.acquisition;
  const lifecycle = data?.lifecycle;
  const acquisitionStages: FunnelStageRailItem[] = acquisition ? [
    { key: "landing", label: t("adminFunnel.acquisition.landing"), count: acquisition.ux_landing_users, detail: t("adminFunnel.acquisition.uxSource") },
    { key: "started", label: t("adminFunnel.acquisition.started"), count: acquisition.ux_signup_started_users, detail: t("adminFunnel.acquisition.uxSource"), rateFromPrevious: rate(acquisition.ux_landing_users, acquisition.ux_signup_started_users) },
    { key: "account", label: t("adminFunnel.acquisition.account"), count: acquisition.accounts_created, detail: t("adminFunnel.acquisition.dbSource") },
    { key: "first-entry", label: t("adminFunnel.acquisition.firstEntry"), count: acquisition.first_authenticated, detail: t("adminFunnel.acquisition.auditSource"), rateFromPrevious: rate(acquisition.accounts_created, acquisition.first_authenticated) },
  ] : [];
  const lifecycleStages: FunnelStageRailItem[] = lifecycle ? [
    { key: "accounts", label: t("adminFunnel.lifecycle.accounts"), count: lifecycle.customer_accounts, detail: t("adminFunnel.lifecycle.usersUnit") },
    { key: "projects", label: t("adminFunnel.lifecycle.projects"), count: lifecycle.project_owners, detail: t("adminFunnel.lifecycle.usersUnit"), rateFromPrevious: rate(lifecycle.customer_accounts, lifecycle.project_owners) },
    { key: "requested", label: t("adminFunnel.lifecycle.requested"), count: lifecycle.resource_requesters, detail: t("adminFunnel.lifecycle.usersUnit"), rateFromPrevious: rate(lifecycle.project_owners, lifecycle.resource_requesters) },
    { key: "ready", label: t("adminFunnel.lifecycle.ready"), count: lifecycle.ready_resource_users, detail: t("adminFunnel.lifecycle.usersUnit"), rateFromPrevious: rate(lifecycle.resource_requesters, lifecycle.ready_resource_users) },
    { key: "resource-orgs", label: t("adminFunnel.lifecycle.resourceOrgs"), count: lifecycle.resource_organizations, detail: t("adminFunnel.lifecycle.orgsUnit") },
    { key: "checkout", label: t("adminFunnel.lifecycle.checkout"), count: lifecycle.checkout_organizations, detail: t("adminFunnel.lifecycle.orgsUnit"), rateFromPrevious: rate(lifecycle.resource_organizations, lifecycle.checkout_organizations) },
    { key: "paid", label: t("adminFunnel.lifecycle.paid"), count: lifecycle.paid_organizations, detail: t("adminFunnel.lifecycle.orgsUnit"), rateFromPrevious: rate(lifecycle.checkout_organizations, lifecycle.paid_organizations) },
  ] : [];

  if (forbidden) return <div><Breadcrumb items={[{ label: t("common.crumb.console"), href: "/projects" }, { label: t("approvals.crumb.admin") }, { label: t("adminFunnel.crumb.funnel") }]} /><div className="mt-4 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300">{t("adminFunnel.accessDenied")}</div></div>;

  return <div>
    <div className="mb-4"><Breadcrumb items={[{ label: t("common.crumb.console"), href: "/projects" }, { label: t("approvals.crumb.admin") }, { label: t("adminFunnel.crumb.funnel") }]} /><h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("adminFunnel.title")}</h1><p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("adminFunnel.subtitle")}</p></div>
    <AdminTabs active="funnel" />
    {error && <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-400">{error}</div>}
    <div className="my-4 flex flex-wrap items-center gap-2"><div className="flex shrink-0 items-center gap-1 rounded-lg bg-gray-100 p-0.5 dark:bg-gray-800">{WINDOWS.map((window) => <button key={window} type="button" onClick={() => setWindowKey(window)} className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${windowKey === window ? "bg-white text-gray-900 shadow-sm dark:bg-gray-700 dark:text-gray-100" : "text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200"}`}>{t(`adminFunnel.window.${window}`)}</button>)}</div></div>

    <section className="mb-6 rounded-xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-800 dark:bg-gray-900">
      <h2 className="text-base font-semibold text-gray-900 dark:text-gray-100">{t("adminFunnel.acquisition.title")}</h2><p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("adminFunnel.acquisition.body")}</p>
      {loading ? <div className="flex h-52 items-center justify-center"><Spinner size="md" /></div> : !traffic?.available ? <p className="mt-4 text-sm text-amber-600 dark:text-amber-400">{traffic?.note || t("adminFunnel.channel.unavailable")}</p> : <><div className="mt-5"><ChannelFunnelSankey channels={trafficSeries} sourceLabel={trafficSourceLabel} stageLabels={[t("adminFunnel.channel.entered"), t("adminFunnel.channel.register"), t("adminFunnel.channel.started"), t("adminFunnel.channel.complete")]} dropLabels={[t("adminFunnel.channel.left"), t("adminFunnel.channel.abandoned"), t("adminFunnel.channel.unfinished")]} clampNote={(sources) => t("adminFunnel.channel.clamped", { sources })} /></div><div className="mt-4 rounded-lg border border-dashed border-gray-200 bg-gray-50 px-3 py-2 text-xs text-gray-500 dark:border-gray-700 dark:bg-gray-800/40 dark:text-gray-400">{t("adminFunnel.acquisition.metrikaBoundary", { visits: traffic.totals.visits, deploys: traffic.totals.deploy_success })}</div><details className="mt-4"><summary className="cursor-pointer text-xs font-medium text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200">{t("adminFunnel.channel.table")}</summary><div className="mt-3 overflow-x-auto"><table className="w-full min-w-[680px] text-sm"><thead className="border-b border-gray-200 text-left text-xs text-gray-500 dark:border-gray-800 dark:text-gray-400"><tr><th className="px-2 py-2">{t("adminFunnel.channel.source")}</th><th className="px-2 py-2 text-right">{t("adminFunnel.channel.visits")}</th><th className="px-2 py-2 text-right">{t("adminFunnel.channel.register")}</th><th className="px-2 py-2 text-right">{t("adminFunnel.channel.started")}</th><th className="px-2 py-2 text-right">{t("adminFunnel.channel.complete")}</th></tr></thead><tbody>{[...traffic.channels, traffic.totals].map((row, index) => <tr key={`${row.source}-${index}`} className="border-t border-gray-100 text-gray-700 dark:border-gray-800 dark:text-gray-300"><td className="px-2 py-2">{index === traffic.channels.length ? t("adminFunnel.channel.total") : trafficSourceLabel(row.source)}</td><td className="px-2 py-2 text-right tabular-nums">{row.visits}</td><td className="px-2 py-2 text-right tabular-nums">{row.register_opened}</td><td className="px-2 py-2 text-right tabular-nums">{row.signup_started}</td><td className="px-2 py-2 text-right tabular-nums">{row.registration_complete}</td></tr>)}</tbody></table></div></details></>}
      {!loading && acquisition && <section className="mt-6 border-t border-gray-100 pt-5 dark:border-gray-800"><h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("adminFunnel.acquisition.confirmedTitle")}</h3><p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{t("adminFunnel.acquisition.confirmedBody")}</p><div className="mt-3"><FunnelStageRail items={acquisitionStages} ariaLabel={t("adminFunnel.acquisition.confirmedTitle")} /></div></section>}
    </section>

    <section className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-800 dark:bg-gray-900">
      <h2 className="text-base font-semibold text-gray-900 dark:text-gray-100">{t("adminFunnel.lifecycle.title")}</h2><p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("adminFunnel.lifecycle.body")}</p>
      {loading ? <div className="flex h-52 items-center justify-center"><Spinner size="md" /></div> : lifecycle && <><div className="mt-5"><FunnelStageRail items={lifecycleStages} ariaLabel={t("adminFunnel.lifecycle.title")} /></div><section className="mt-6 border-t border-gray-100 pt-5 dark:border-gray-800"><h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("adminFunnel.lifecycle.resourcesTitle")}</h3><p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{t("adminFunnel.lifecycle.resourcesBody")}</p><div className="mt-3 grid gap-2 sm:grid-cols-2 lg:grid-cols-5">{lifecycle.resources.map((resource) => <div key={resource.key} className="rounded-lg bg-gray-50 p-3 dark:bg-gray-800/50"><p className="text-sm font-medium text-gray-900 dark:text-gray-100">{resourceLabel(resource, t)}</p><dl className="mt-2 space-y-1 text-xs text-gray-500 dark:text-gray-400"><div className="flex justify-between gap-2"><dt>{t("adminFunnel.lifecycle.resourceUsers")}</dt><dd className="tabular-nums text-gray-700 dark:text-gray-200">{resource.requested_users}</dd></div><div className="flex justify-between gap-2"><dt>{t("adminFunnel.lifecycle.resourceRequests")}</dt><dd className="tabular-nums text-gray-700 dark:text-gray-200">{resource.requests}</dd></div><div className="flex justify-between gap-2"><dt>{t("adminFunnel.lifecycle.resourceReady")}</dt><dd className="tabular-nums text-gray-700 dark:text-gray-200">{resource.ready_users}</dd></div></dl></div>)}</div></section><section className="mt-6 border-t border-gray-100 pt-5 dark:border-gray-800"><h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("adminFunnel.lifecycle.quotaTitle")}</h3><p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{t("adminFunnel.lifecycle.quotaBody")}</p><dl className="mt-3 grid gap-2 sm:grid-cols-3"><div className="rounded-lg bg-amber-50 p-3 dark:bg-amber-950/30"><dt className="text-xs text-amber-700 dark:text-amber-300">{t("adminFunnel.lifecycle.quotaUsers")}</dt><dd className="mt-1 text-xl font-semibold tabular-nums text-amber-900 dark:text-amber-100">{lifecycle.quota_blocked_users}</dd></div><div className="rounded-lg bg-amber-50 p-3 dark:bg-amber-950/30"><dt className="text-xs text-amber-700 dark:text-amber-300">{t("adminFunnel.lifecycle.quotaAttempts")}</dt><dd className="mt-1 text-xl font-semibold tabular-nums text-amber-900 dark:text-amber-100">{lifecycle.quota_blocked_attempts}</dd></div><div className="rounded-lg bg-amber-50 p-3 dark:bg-amber-950/30"><dt className="text-xs text-amber-700 dark:text-amber-300">{t("adminFunnel.lifecycle.quotaGrace")}</dt><dd className="mt-1 text-xl font-semibold tabular-nums text-amber-900 dark:text-amber-100">{lifecycle.quota_grace_organizations}</dd></div></dl></section></>}
    </section>
  </div>;
}

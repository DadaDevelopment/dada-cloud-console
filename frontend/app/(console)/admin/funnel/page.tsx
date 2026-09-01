"use client";

import { useCallback, useEffect, useState } from "react";
import { adminApi } from "@/lib/api";
import type { AdminFunnelResponse, AdminFunnelResource } from "@/lib/types";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { AdminTabs } from "@/components/console/admin-tabs";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";
import { DetailedFunnelSankey, type DetailedFunnelStream } from "@/components/console/detailed-funnel-sankey";

const WINDOWS = ["7d", "30d", "90d", "all"] as const;

function trafficSourceLabel(source: string): string {
  const labels: Record<string, string> = { "Direct traffic": "Прямой", "Internal traffic": "Внутренний", "Search engine traffic": "Поиск", "Link traffic": "Ссылки", "Messenger traffic": "Мессенджеры", "Recommendation system traffic": "Рекомендации" };
  return labels[source] ?? source;
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
  const load = useCallback(async () => { setLoading(true); setError(null); try { setData(await adminApi.getFunnel({ window: windowKey })); setForbidden(false); } catch (err) { if ((err as { status?: number }).status === 403) setForbidden(true); else setError(err instanceof Error ? err.message : t("adminFunnel.error.load")); } finally { setLoading(false); } }, [t, windowKey]);

  useEffect(() => { const timer = window.setTimeout(() => { void load(); }, 0); return () => window.clearTimeout(timer); }, [load]);

  const traffic = data?.channel_funnel;
  const kc = data?.kc_funnel;
  const acquisition = data?.acquisition;
  const lifecycle = data?.lifecycle;
  const kcStage = (stage: { label: string; count: number }, unit?: string) => ({
    label: stage.label,
    value: stage.count,
    ...(unit ? { detail: unit } : {}),
  });
  const kcStreamLabel = t("adminFunnel.kc.stream");
  const kcStreamColor = "#b45309";
  const kcRegistered = kc?.channels.reduce((sum, ch) => sum + ch.count, 0) ?? 0;
  const acquisitionStreams: DetailedFunnelStream[] = [
    ...(kc?.available ? [{
      id: "kc-unified",
      label: kcStreamLabel,
      color: kcStreamColor,
      stages: [
        kcStage(kc.login[0]),
        kcStage(kc.native[0]),
        kcStage(kc.native[4]),
        kcStage(kc.yandex[0]),
        {
          label: t("adminFunnel.kc.registered"),
          value: kcRegistered,
          detail: t("adminFunnel.kc.dbSource"),
        },
      ],
    }] : []),
    ...(traffic?.available ? [{ id: "metrika", label: t("adminFunnel.acquisition.metrikaStream"), color: "#2563eb", stages: [{ label: t("adminFunnel.channel.entered"), value: traffic.totals.users, detail: t("adminFunnel.acquisition.metrikaUsers") }, { label: t("adminFunnel.channel.register"), value: traffic.totals.register_opened }, { label: t("adminFunnel.channel.started"), value: traffic.totals.signup_started }, { label: t("adminFunnel.channel.complete"), value: traffic.totals.registration_complete }] }] : []),
    ...(kc?.available ? [{
      id: "kc-yandex",
      label: t("adminFunnel.kc.yandexLeg"),
      color: kcStreamColor,
      stages: [
        kcStage(kc.login[0], t("adminFunnel.kc.sharedEntry")),
        kcStage(kc.yandex[0]),
        kcStage(kc.yandex[3], t("adminFunnel.kc.legNote")),
      ],
    }, {
      id: "kc-native",
      label: t("adminFunnel.kc.nativeLeg"),
      color: kcStreamColor,
      stages: [
        kcStage(kc.login[0], t("adminFunnel.kc.sharedEntry")),
        kcStage(kc.native[0]),
        kcStage(kc.native[4], t("adminFunnel.kc.legNote")),
      ],
    }] : []),
    ...(acquisition ? [{ id: "ux", label: t("adminFunnel.acquisition.uxStream"), color: "#7c3aed", stages: [{ label: t("adminFunnel.acquisition.landing"), value: acquisition.ux_landing_users, detail: t("adminFunnel.acquisition.uxSource") }, { label: t("adminFunnel.acquisition.started"), value: acquisition.ux_signup_started_users, detail: t("adminFunnel.acquisition.uxSource") }] }, { id: "account", label: t("adminFunnel.acquisition.accountStream"), startColumn: 3, color: "#0f766e", stages: [{ label: t("adminFunnel.acquisition.account"), value: acquisition.accounts_created, detail: t("adminFunnel.acquisition.dbSource") }, { label: t("adminFunnel.acquisition.firstEntry"), value: acquisition.first_authenticated, detail: t("adminFunnel.acquisition.auditSource") }] }] : []),
  ];
  const lifecycleStreams: DetailedFunnelStream[] = lifecycle ? [
    { id: "customer", label: t("adminFunnel.lifecycle.userStream"), color: "#2563eb", stages: [{ label: t("adminFunnel.lifecycle.accounts"), value: lifecycle.customer_accounts, detail: t("adminFunnel.lifecycle.usersUnit") }, { label: t("adminFunnel.lifecycle.projects"), value: lifecycle.project_owners, detail: t("adminFunnel.lifecycle.usersUnit") }, { label: t("adminFunnel.lifecycle.requested"), value: lifecycle.resource_requesters, detail: t("adminFunnel.lifecycle.usersUnit") }, { label: t("adminFunnel.lifecycle.ready"), value: lifecycle.ready_resource_users, detail: t("adminFunnel.lifecycle.usersUnit") }] },
    { id: "first-deploy", label: t("adminFunnel.lifecycle.firstDeployStream"), color: "#ea580c", stages: [{ label: t("adminFunnel.lifecycle.gitConnected"), value: lifecycle.git_connected_users, detail: t("adminFunnel.lifecycle.usersUnit") }, { label: t("adminFunnel.lifecycle.buildStarted"), value: lifecycle.build_started_users, detail: t("adminFunnel.lifecycle.usersUnit") }, { label: t("adminFunnel.lifecycle.appCreated"), value: lifecycle.app_created_users, detail: t("adminFunnel.lifecycle.usersUnit") }, { label: t("adminFunnel.lifecycle.firstDeployed"), value: lifecycle.first_deployed_users, detail: t("adminFunnel.lifecycle.usersUnit") }] },
    ...lifecycle.resources.map((resource, index) => ({ id: `resource:${resource.key}`, label: resourceLabel(resource, t), startColumn: 2, color: ["#7c3aed", "#0f766e", "#d97706", "#db2777", "#0891b2"][index % 5], stages: [{ label: t("adminFunnel.lifecycle.resourceUsers"), value: resource.requested_users, detail: `${t("adminFunnel.lifecycle.resourceRequests")}: ${resource.requests}` }, { label: t("adminFunnel.lifecycle.resourceReady"), value: resource.ready_users, detail: t("adminFunnel.lifecycle.resourcesOverlap") }] })),
    { id: "billing", label: t("adminFunnel.lifecycle.billingStream"), startColumn: 4, color: "#059669", stages: [{ label: t("adminFunnel.lifecycle.resourceOrgs"), value: lifecycle.resource_organizations, detail: t("adminFunnel.lifecycle.orgsUnit") }, { label: t("adminFunnel.lifecycle.checkout"), value: lifecycle.checkout_organizations, detail: t("adminFunnel.lifecycle.orgsUnit") }, { label: t("adminFunnel.lifecycle.paid"), value: lifecycle.paid_organizations, detail: t("adminFunnel.lifecycle.orgsUnit") }] },
  ] : [];
  const crumbs = [{ label: t("common.crumb.console"), href: "/projects" }, { label: t("approvals.crumb.admin") }, { label: t("adminFunnel.crumb.funnel") }];
  if (forbidden) return <div><Breadcrumb items={crumbs} /><div className="mt-4 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300">{t("adminFunnel.accessDenied")}</div></div>;

  return <div>
    <div className="mb-4"><Breadcrumb items={crumbs} /><h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("adminFunnel.title")}</h1><p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("adminFunnel.subtitle")}</p></div>
    <AdminTabs active="funnel" />
    {error && <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-400">{error}</div>}
    <div className="my-4 flex flex-wrap items-center gap-2"><div className="flex shrink-0 items-center gap-1 rounded-lg bg-gray-100 p-0.5 dark:bg-gray-800">{WINDOWS.map((window) => <button key={window} type="button" onClick={() => setWindowKey(window)} className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${windowKey === window ? "bg-white text-gray-900 shadow-sm dark:bg-gray-700 dark:text-gray-100" : "text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200"}`}>{t(`adminFunnel.window.${window}`)}</button>)}</div></div>
    <section className="mb-6 rounded-xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-800 dark:bg-gray-900"><h2 className="text-base font-semibold text-gray-900 dark:text-gray-100">{t("adminFunnel.acquisition.title")}</h2><p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("adminFunnel.acquisition.body")}</p>{loading ? <div className="flex h-72 items-center justify-center"><Spinner size="md" /></div> : <><div className="mt-5"><DetailedFunnelSankey streams={acquisitionStreams} annotations={[{ label: t("adminFunnel.channel.visits"), value: traffic?.totals.visits ?? 0, detail: t("adminFunnel.acquisition.visitsDetail") }, { label: t("adminFunnel.channel.deploy"), value: traffic?.totals.deploy_success ?? 0, detail: t("adminFunnel.acquisition.deployDetail") }]} ariaLabel={t("adminFunnel.acquisition.title")} /></div>{!traffic?.available && <p className="mt-4 text-sm text-amber-600 dark:text-amber-400">{traffic?.note || t("adminFunnel.channel.unavailable")}</p>}<details className="mt-5 border-t border-gray-100 pt-4 dark:border-gray-800"><summary className="cursor-pointer text-xs font-medium text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200">{t("adminFunnel.channel.table")}</summary><div className="mt-3 overflow-x-auto"><table className="w-full min-w-[680px] text-sm"><thead className="border-b border-gray-200 text-left text-xs text-gray-500 dark:border-gray-800 dark:text-gray-400"><tr><th className="px-2 py-2">{t("adminFunnel.channel.source")}</th><th className="px-2 py-2 text-right">{t("adminFunnel.channel.visits")}</th><th className="px-2 py-2 text-right">{t("adminFunnel.channel.register")}</th><th className="px-2 py-2 text-right">{t("adminFunnel.channel.started")}</th><th className="px-2 py-2 text-right">{t("adminFunnel.channel.complete")}</th></tr></thead><tbody>{traffic && [...traffic.channels, traffic.totals].map((row, index) => <tr key={`${row.source}-${index}`} className="border-t border-gray-100 text-gray-700 dark:border-gray-800 dark:text-gray-300"><td className="px-2 py-2">{index === traffic.channels.length ? t("adminFunnel.channel.total") : trafficSourceLabel(row.source)}</td><td className="px-2 py-2 text-right tabular-nums">{row.visits}</td><td className="px-2 py-2 text-right tabular-nums">{row.register_opened}</td><td className="px-2 py-2 text-right tabular-nums">{row.signup_started}</td><td className="px-2 py-2 text-right tabular-nums">{row.registration_complete}</td></tr>)}</tbody></table></div></details></>}</section>
    <section className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-800 dark:bg-gray-900"><h2 className="text-base font-semibold text-gray-900 dark:text-gray-100">{t("adminFunnel.lifecycle.title")}</h2><p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("adminFunnel.lifecycle.body")}</p>{loading ? <div className="flex h-96 items-center justify-center"><Spinner size="md" /></div> : lifecycle && <div className="mt-5"><DetailedFunnelSankey streams={lifecycleStreams} annotations={[{ label: t("adminFunnel.lifecycle.quotaUsers"), value: lifecycle.quota_blocked_users, detail: t("adminFunnel.lifecycle.quotaUsersDetail") }, { label: t("adminFunnel.lifecycle.quotaAttempts"), value: lifecycle.quota_blocked_attempts, detail: t("adminFunnel.lifecycle.quotaAttemptsDetail") }, { label: t("adminFunnel.lifecycle.quotaGrace"), value: lifecycle.quota_grace_organizations, detail: t("adminFunnel.lifecycle.quotaGraceDetail") }]} ariaLabel={t("adminFunnel.lifecycle.title")} /></div>}</section>
  </div>;
}

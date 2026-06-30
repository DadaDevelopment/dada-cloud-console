"use client";
import { useEffect, useState, FormEvent } from "react";
import { useParams, useSearchParams } from "next/navigation";
import Link from "next/link";
import { monitoringApi } from "@/lib/api";
import type {
  MonitoringApp,
  HealthStatus,
  HealthState,
  AlertRule,
  Channel,
} from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { MetricsPanel } from "@/components/metrics-panel";
import { LogsViewer } from "@/components/logs-viewer";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { useT } from "@/lib/i18n/console/context";

type Tab = "overview" | "metrics" | "logs" | "alerts";

const TABS: Tab[] = ["overview", "metrics", "logs", "alerts"];

function HealthBadge({ state, critical }: { state: HealthState; critical: boolean }) {
  const colors: Record<HealthState, string> = {
    healthy: "bg-green-100 text-green-800",
    degraded: "bg-yellow-100 text-yellow-800",
    down: "bg-red-100 text-red-800",
    unknown: "bg-gray-100 text-gray-600",
  };
  return (
    <div className="flex items-center gap-2">
      <span className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${colors[state]}`}>
        {state}
      </span>
      {critical && (
        <span className="inline-flex items-center rounded-full bg-red-600 px-2.5 py-0.5 text-xs font-bold text-white uppercase tracking-wide">
          CRITICAL
        </span>
      )}
    </div>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-start justify-between gap-4 border-b border-gray-100 py-2 last:border-0">
      <span className="text-xs font-medium uppercase tracking-wide text-gray-400">{label}</span>
      <span className={`text-sm text-gray-900 break-all text-right ${mono ? "font-mono" : ""}`}>{value}</span>
    </div>
  );
}

function ErrorBox({ text }: { text: string }) {
  return (
    <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{text}</div>
  );
}

function ModalFooter({
  onCancel,
  submitting,
  submitLabel,
  tone = "blue",
}: {
  onCancel: () => void;
  submitting: boolean;
  submitLabel: string;
  tone?: "blue" | "red";
}) {
  const { t } = useT();
  const tones = {
    blue: "bg-blue-600 hover:bg-blue-700",
    red: "bg-red-600 hover:bg-red-700",
  };
  return (
    <div className="flex justify-end gap-3 pt-2">
      <button
        type="button"
        onClick={onCancel}
        className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100 transition-colors"
      >
        {t("common.cancel")}
      </button>
      <button
        type="submit"
        disabled={submitting}
        className={`inline-flex items-center gap-2 rounded-lg px-4 py-2 text-sm font-medium text-white ${tones[tone]} disabled:cursor-not-allowed disabled:opacity-50 transition-colors`}
      >
        {submitting ? (
          <>
            <Spinner size="sm" /> {t("monitoring.detail.modal.rule.working")}
          </>
        ) : (
          submitLabel
        )}
      </button>
    </div>
  );
}

export default function MonitoringDetailPage() {
  const { t } = useT();
  const params = useParams<{ projectId: string; appId: string }>();
  const search = useSearchParams();
  const { projectId, appId } = params;
  const { selectedEnv, role } = useProjectContext();
  const envId = search.get("envId") || selectedEnv?.id || "";

  const requestedTab = search.get("tab") as Tab | null;
  const initialTab: Tab = requestedTab && TABS.includes(requestedTab) ? requestedTab : "overview";
  const [tab, setTab] = useState<Tab>(initialTab);

  const [app, setApp] = useState<MonitoringApp | null>(null);
  const [health, setHealth] = useState<HealthStatus | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isGrafanaLoading, setIsGrafanaLoading] = useState(false);

  useEffect(() => {
    if (!envId) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setError(t("monitoring.detail.missingEnv"));
      setIsLoading(false);
      return;
    }
    Promise.all([
      monitoringApi.get(projectId, envId, appId),
      monitoringApi.getHealth(projectId, envId, appId).catch(() => null),
    ])
      .then(([d, h]) => {
        setApp(d.app);
        setHealth(h);
      })
      .catch((err) => setError(err instanceof Error ? err.message : t("monitoring.detail.error.loadApp")))
      .finally(() => setIsLoading(false));
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, envId, appId]);

  async function openGrafana() {
    setIsGrafanaLoading(true);
    try {
      const r = await monitoringApi.getGrafanaLink(projectId, envId, appId);
      window.open(r.url, "_blank", "noopener,noreferrer");
    } catch {
    } finally {
      setIsGrafanaLoading(false);
    }
  }

  if (isLoading) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner size="lg" />
      </div>
    );
  }
  if (error || !app) {
    return (
      <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
        {error ?? t("monitoring.detail.notFound")}
      </div>
    );
  }

  const tabs: { key: Tab; label: string }[] = [
    { key: "overview", label: t("monitoring.detail.tab.overview") },
    { key: "metrics", label: t("monitoring.detail.tab.metrics") },
    { key: "logs", label: t("monitoring.detail.tab.logs") },
    { key: "alerts", label: t("monitoring.detail.tab.alerts") },
  ];

  return (
    <div>
      <div className="mb-6 flex items-start justify-between">
        <div>
          <div className="flex items-center gap-2 text-sm text-gray-500">
            <Link href="/projects" className="hover:text-gray-700">{t("common.crumb.projects")}</Link>
            <span>/</span>
            <Link href={`/projects/${projectId}`} className="hover:text-gray-700">{t("common.crumb.overview")}</Link>
            <span>/</span>
            <Link href={`/projects/${projectId}/monitoring`} className="hover:text-gray-700">{t("nav.monitoring")}</Link>
            <span>/</span>
            <span className="font-mono text-gray-900">{app.name}</span>
          </div>
          <div className="mt-2 flex items-center gap-3">
            <h1 className="font-mono text-2xl font-bold text-gray-900">{app.name}</h1>
            {health && <HealthBadge state={health.state} critical={health.critical} />}
          </div>
        </div>
        <button
          onClick={openGrafana}
          disabled={isGrafanaLoading}
          className="inline-flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:border-orange-300 hover:text-orange-600 transition-colors shadow-sm disabled:opacity-50"
        >
          {isGrafanaLoading ? <Spinner size="sm" /> : (
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
            </svg>
          )}
          {t("monitoring.detail.openInGrafana")}
        </button>
      </div>

      <div className="mb-6 border-b border-gray-200">
        <nav className="-mb-px flex gap-6">
          {tabs.map((tabItem) => {
            const active = tab === tabItem.key;
            return (
              <button
                key={tabItem.key}
                onClick={() => setTab(tabItem.key)}
                className={`border-b-2 px-1 py-3 text-sm font-medium transition-colors ${
                  active
                    ? "border-blue-600 text-blue-600"
                    : "border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-700"
                }`}
              >
                {tabItem.label}
              </button>
            );
          })}
        </nav>
      </div>

      {tab === "overview" && (
        <OverviewTab
          app={app}
          health={health}
          projectId={projectId}
          envId={envId}
          appId={appId}
        />
      )}
      {tab === "metrics" && (
        <MetricsPanel kind="monitoring" projectId={projectId} envId={envId} appId={appId} />
      )}
      {tab === "logs" && (
        <LogsViewer
          projectId={projectId}
          monitoring={{ projectId, envId, appId }}
        />
      )}
      {tab === "alerts" && (
        <AlertsTab
          projectId={projectId}
          envId={envId}
          appId={appId}
          role={role}
        />
      )}
    </div>
  );
}

function OverviewTab({
  app,
  health,
  projectId,
  envId,
  appId,
}: {
  app: MonitoringApp;
  health: HealthStatus | null;
  projectId: string;
  envId: string;
  appId: string;
}) {
  const { t } = useT();
  return (
    <div className="space-y-6">
      {health && (
        <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
          <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">{t("monitoring.detail.health.title")}</h2>
          <div className="space-y-1">
            <Row label={t("monitoring.detail.health.state")} value={health.state} />
            <Row label={t("monitoring.detail.health.lastSeen")} value={health.last_seen ? new Date(health.last_seen).toLocaleString() : "—"} />
            <Row label={t("monitoring.detail.health.errorRate")} value={`${(health.error_rate_15m * 100).toFixed(2)}%`} />
            <Row label={t("monitoring.detail.health.firingAlerts")} value={String(health.firing_alerts)} />
          </div>
          {health.reasons.length > 0 && (
            <div className="mt-3">
              <p className="text-xs font-medium uppercase tracking-wide text-gray-400 mb-1">{t("monitoring.detail.health.reasons")}</p>
              <ul className="space-y-1">
                {health.reasons.map((r, i) => (
                  <li key={i} className="text-sm text-gray-700 flex items-start gap-2">
                    <span className="mt-1 h-1.5 w-1.5 shrink-0 rounded-full bg-red-400" />
                    {r}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}

      <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">{t("monitoring.detail.info.title")}</h2>
        <Row label={t("monitoring.detail.info.id")} value={app.id} mono />
        <Row label={t("monitoring.detail.info.created")} value={new Date(app.created_at).toLocaleString()} />
        <Row label={t("monitoring.detail.info.updated")} value={new Date(app.updated_at).toLocaleString()} />
        {app.grafana_dashboard_uid && (
          <Row label={t("monitoring.detail.info.grafanaUid")} value={app.grafana_dashboard_uid} mono />
        )}
      </div>

      <MetricsPanel kind="monitoring" projectId={projectId} envId={envId} appId={appId} />

      <LogsViewer
        projectId={projectId}
        monitoring={{ projectId, envId, appId }}
      />
    </div>
  );
}

function AlertsTab({
  projectId,
  envId,
  appId,
  role,
}: {
  projectId: string;
  envId: string;
  appId: string;
  role: import("@/lib/types").MemberRole | undefined;
}) {
  const { t } = useT();
  const [rules, setRules] = useState<AlertRule[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isRuleModalOpen, setIsRuleModalOpen] = useState(false);
  const [metricOptions, setMetricOptions] = useState<string[]>([]);
  const [ruleForm, setRuleForm] = useState({
    name: "",
    metric: "",
    condition: ">",
    threshold: 80,
    duration: "5m",
    channel_id: "",
  });
  const [isRuleSubmitting, setIsRuleSubmitting] = useState(false);
  const [ruleError, setRuleError] = useState<string | null>(null);

  const [isChannelModalOpen, setIsChannelModalOpen] = useState(false);
  const [channelForm, setChannelForm] = useState({
    name: "",
    type: "telegram" as "telegram" | "email" | "webhook",
    bot_token: "",
    chat_id: "",
    addresses: "",
    url: "",
  });
  const [isChannelSubmitting, setIsChannelSubmitting] = useState(false);
  const [channelError, setChannelError] = useState<string | null>(null);

  const canWrite = canMutate(role);

  useEffect(() => {
    Promise.all([
      monitoringApi.listAlertRules(projectId, envId, appId),
      monitoringApi.listChannels(projectId, envId),
    ])
      .then(([r, c]) => {
        setRules(r.rules ?? []);
        setChannels(c.channels ?? []);
      })
      .catch((err) => setError(err instanceof Error ? err.message : t("monitoring.detail.modal.rule.error.load")))
      .finally(() => setIsLoading(false));
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, envId, appId]);

  // Discover the resource's actual metric names so the rule form suggests real
  // keys instead of a hardcoded cpu/memory/temp list. Best-effort: a device that
  // sends arbitrary metrics still gets accurate suggestions, and the field stays
  // free-text so any not-yet-seen metric can be targeted.
  useEffect(() => {
    monitoringApi
      .getMetrics(projectId, envId, appId, { range: "24h" })
      .then((d) => setMetricOptions(Object.keys(d.metrics ?? {}).sort()))
      .catch(() => setMetricOptions([]));
  }, [projectId, envId, appId]);

  async function deleteRule(ruleId: string) {
    try {
      await monitoringApi.deleteAlertRule(projectId, envId, appId, ruleId);
      setRules((prev) => prev.filter((r) => r.id !== ruleId));
    } catch {
    }
  }

  async function deleteChannel(id: string) {
    try {
      await monitoringApi.deleteChannel(projectId, envId, id);
      setChannels((prev) => prev.filter((c) => c.id !== id));
    } catch {
    }
  }

  async function submitRule(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setRuleError(null);
    setIsRuleSubmitting(true);
    try {
      const r = await monitoringApi.createAlertRule(projectId, envId, appId, {
        name: ruleForm.name,
        metric: ruleForm.metric.trim(),
        condition: ruleForm.condition,
        threshold: ruleForm.threshold,
        duration: ruleForm.duration,
        channel_id: ruleForm.channel_id || undefined,
      });
      setRules((prev) => [...prev, r.rule]);
      setIsRuleModalOpen(false);
      setRuleForm({ name: "", metric: "", condition: ">", threshold: 80, duration: "5m", channel_id: "" });
    } catch (err) {
      setRuleError(err instanceof Error ? err.message : t("monitoring.detail.modal.rule.error"));
    } finally {
      setIsRuleSubmitting(false);
    }
  }

  async function submitChannel(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setChannelError(null);
    setIsChannelSubmitting(true);
    try {
      let settings: Record<string, string> = {};
      if (channelForm.type === "telegram") {
        settings = { bot_token: channelForm.bot_token, chat_id: channelForm.chat_id };
      } else if (channelForm.type === "email") {
        settings = { addresses: channelForm.addresses };
      } else {
        settings = { url: channelForm.url };
      }
      const r = await monitoringApi.createChannel(projectId, envId, {
        name: channelForm.name,
        type: channelForm.type,
        settings,
      });
      setChannels((prev) => [...prev, r.channel]);
      setIsChannelModalOpen(false);
      setChannelForm({ name: "", type: "telegram", bot_token: "", chat_id: "", addresses: "", url: "" });
    } catch (err) {
      setChannelError(err instanceof Error ? err.message : t("monitoring.detail.modal.channel.error"));
    } finally {
      setIsChannelSubmitting(false);
    }
  }

  if (isLoading) {
    return (
      <div className="flex h-40 items-center justify-center">
        <Spinner />
      </div>
    );
  }

  if (error) {
    return <ErrorBox text={error} />;
  }

  return (
    <div className="space-y-8">
      <div>
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-500">{t("monitoring.detail.alerts.section")}</h2>
          {canWrite && (
            <button
              onClick={() => setIsRuleModalOpen(true)}
              className="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
            >
              <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
              </svg>
              {t("monitoring.detail.alerts.createRule")}
            </button>
          )}
        </div>

        {rules.length === 0 ? (
          <div className="rounded-xl border border-dashed border-gray-300 bg-gray-50 py-10 text-center">
            <p className="text-sm text-gray-400">{t("monitoring.detail.alerts.empty")}</p>
          </div>
        ) : (
          <div className="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm">
            <table className="min-w-full divide-y divide-gray-200">
              <thead className="bg-gray-50">
                <tr>
                  <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">{t("monitoring.detail.alerts.col.name")}</th>
                  <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">{t("monitoring.detail.alerts.col.metric")}</th>
                  <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">{t("monitoring.detail.alerts.col.condition")}</th>
                  <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">{t("monitoring.detail.alerts.col.duration")}</th>
                  <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">{t("monitoring.detail.alerts.col.channel")}</th>
                  {canWrite && <th className="px-5 py-3" />}
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {rules.map((rule) => (
                  <tr key={rule.id}>
                    <td className="px-5 py-3 text-sm font-medium text-gray-900">{rule.name}</td>
                    <td className="px-5 py-3 font-mono text-sm text-gray-700">{rule.metric}</td>
                    <td className="px-5 py-3 font-mono text-sm text-gray-700">
                      {rule.condition} {rule.threshold}
                    </td>
                    <td className="px-5 py-3 text-sm text-gray-700">{rule.duration}</td>
                    <td className="px-5 py-3 text-sm text-gray-500">{rule.channel_name ?? "—"}</td>
                    {canWrite && (
                      <td className="px-5 py-3 text-right">
                        <button
                          onClick={() => deleteRule(rule.id)}
                          className="text-xs text-red-500 hover:text-red-700 transition-colors"
                        >
                          {t("monitoring.detail.alerts.deleteRule")}
                        </button>
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <div>
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-500">{t("monitoring.detail.channels.section")}</h2>
          {canWrite && (
            <button
              onClick={() => setIsChannelModalOpen(true)}
              className="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
            >
              <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
              </svg>
              {t("monitoring.detail.channels.add")}
            </button>
          )}
        </div>

        {channels.length === 0 ? (
          <div className="rounded-xl border border-dashed border-gray-300 bg-gray-50 py-10 text-center">
            <p className="text-sm text-gray-400">{t("monitoring.detail.channels.empty")}</p>
          </div>
        ) : (
          <div className="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm">
            <table className="min-w-full divide-y divide-gray-200">
              <thead className="bg-gray-50">
                <tr>
                  <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">{t("monitoring.detail.channels.col.name")}</th>
                  <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">{t("monitoring.detail.channels.col.type")}</th>
                  <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">{t("monitoring.detail.channels.col.created")}</th>
                  {canWrite && <th className="px-5 py-3" />}
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {channels.map((ch) => (
                  <tr key={ch.id}>
                    <td className="px-5 py-3 text-sm font-medium text-gray-900">{ch.name}</td>
                    <td className="px-5 py-3">
                      <span className="inline-flex items-center rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-700">
                        {ch.type}
                      </span>
                    </td>
                    <td className="px-5 py-3 text-xs text-gray-400">
                      {new Date(ch.created_at).toLocaleDateString()}
                    </td>
                    {canWrite && (
                      <td className="px-5 py-3 text-right">
                        <button
                          onClick={() => deleteChannel(ch.id)}
                          className="text-xs text-red-500 hover:text-red-700 transition-colors"
                        >
                          {t("monitoring.detail.channels.delete")}
                        </button>
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <Modal
        isOpen={isRuleModalOpen}
        onClose={() => {
          setIsRuleModalOpen(false);
          setRuleError(null);
        }}
        title={t("monitoring.detail.modal.createRule.title")}
      >
        <form onSubmit={submitRule} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700">{t("monitoring.detail.modal.rule.name")}</label>
            <input
              type="text"
              required
              value={ruleForm.name}
              onChange={(e) => setRuleForm((p) => ({ ...p, name: e.target.value }))}
              placeholder="high-cpu"
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm font-medium text-gray-700">{t("monitoring.detail.modal.rule.metric")}</label>
              <input
                type="text"
                required
                list="monitoring-metric-options"
                value={ruleForm.metric}
                onChange={(e) => setRuleForm((p) => ({ ...p, metric: e.target.value }))}
                placeholder={metricOptions[0] ?? "metric_name"}
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
              <datalist id="monitoring-metric-options">
                {metricOptions.map((m) => (
                  <option key={m} value={m} />
                ))}
              </datalist>
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700">{t("monitoring.detail.modal.rule.condition")}</label>
              <select
                value={ruleForm.condition}
                onChange={(e) => setRuleForm((p) => ({ ...p, condition: e.target.value }))}
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              >
                <option value=">">&gt;</option>
                <option value="<">&lt;</option>
                <option value=">=">&gt;=</option>
                <option value="<=">&lt;=</option>
              </select>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm font-medium text-gray-700">{t("monitoring.detail.modal.rule.threshold")}</label>
              <input
                type="number"
                required
                value={ruleForm.threshold}
                onChange={(e) => setRuleForm((p) => ({ ...p, threshold: parseFloat(e.target.value) }))}
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700">{t("monitoring.detail.modal.rule.duration")}</label>
              <select
                value={ruleForm.duration}
                onChange={(e) => setRuleForm((p) => ({ ...p, duration: e.target.value }))}
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              >
                <option value="5m">5m</option>
                <option value="10m">10m</option>
                <option value="15m">15m</option>
                <option value="1h">1h</option>
              </select>
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700">
              {t("monitoring.detail.modal.rule.channel")} <span className="text-gray-400 font-normal">{t("common.optional")}</span>
            </label>
            <select
              value={ruleForm.channel_id}
              onChange={(e) => setRuleForm((p) => ({ ...p, channel_id: e.target.value }))}
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="">{t("monitoring.detail.modal.rule.channelNone")}</option>
              {channels.map((ch) => (
                <option key={ch.id} value={ch.id}>
                  {ch.name} ({ch.type})
                </option>
              ))}
            </select>
          </div>

          {ruleError && <ErrorBox text={ruleError} />}
          <ModalFooter
            onCancel={() => {
              setIsRuleModalOpen(false);
              setRuleError(null);
            }}
            submitting={isRuleSubmitting}
            submitLabel={t("monitoring.detail.modal.rule.submitLabel")}
          />
        </form>
      </Modal>

      <Modal
        isOpen={isChannelModalOpen}
        onClose={() => {
          setIsChannelModalOpen(false);
          setChannelError(null);
        }}
        title={t("monitoring.detail.modal.addChannel.title")}
      >
        <form onSubmit={submitChannel} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700">{t("monitoring.detail.modal.channel.name")}</label>
            <input
              type="text"
              required
              value={channelForm.name}
              onChange={(e) => setChannelForm((p) => ({ ...p, name: e.target.value }))}
              placeholder="my-telegram-channel"
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700">{t("monitoring.detail.modal.channel.type")}</label>
            <select
              value={channelForm.type}
              onChange={(e) =>
                setChannelForm((p) => ({
                  ...p,
                  type: e.target.value as "telegram" | "email" | "webhook",
                }))
              }
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="telegram">Telegram</option>
              <option value="email">Email</option>
              <option value="webhook">Webhook</option>
            </select>
          </div>

          {channelForm.type === "telegram" && (
            <>
              <div>
                <label className="block text-sm font-medium text-gray-700">{t("monitoring.detail.modal.channel.botToken")}</label>
                <input
                  type="text"
                  required
                  value={channelForm.bot_token}
                  onChange={(e) => setChannelForm((p) => ({ ...p, bot_token: e.target.value }))}
                  placeholder="123456:ABC-..."
                  className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700">{t("monitoring.detail.modal.channel.chatId")}</label>
                <input
                  type="text"
                  required
                  value={channelForm.chat_id}
                  onChange={(e) => setChannelForm((p) => ({ ...p, chat_id: e.target.value }))}
                  placeholder="-100123456789"
                  className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
            </>
          )}

          {channelForm.type === "email" && (
            <div>
              <label className="block text-sm font-medium text-gray-700">
                {t("monitoring.detail.modal.channel.emailAddresses")} <span className="text-gray-400 font-normal">{t("monitoring.detail.modal.channel.emailHint")}</span>
              </label>
              <input
                type="text"
                required
                value={channelForm.addresses}
                onChange={(e) => setChannelForm((p) => ({ ...p, addresses: e.target.value }))}
                placeholder="ops@acme.com, oncall@acme.com"
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          )}

          {channelForm.type === "webhook" && (
            <div>
              <label className="block text-sm font-medium text-gray-700">{t("monitoring.detail.modal.channel.webhookUrl")}</label>
              <input
                type="url"
                required
                value={channelForm.url}
                onChange={(e) => setChannelForm((p) => ({ ...p, url: e.target.value }))}
                placeholder="https://hooks.example.com/alert"
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          )}

          {channelError && <ErrorBox text={channelError} />}
          <ModalFooter
            onCancel={() => {
              setIsChannelModalOpen(false);
              setChannelError(null);
            }}
            submitting={isChannelSubmitting}
            submitLabel={t("monitoring.detail.modal.channel.submitLabel")}
          />
        </form>
      </Modal>
    </div>
  );
}

"use client";
import { useEffect, useState, useRef, FormEvent } from "react";
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

type Tab = "overview" | "dashboard" | "metrics" | "logs" | "alerts";

// ── Helpers ────────────────────────────────────────────────────────────────────

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
        Cancel
      </button>
      <button
        type="submit"
        disabled={submitting}
        className={`inline-flex items-center gap-2 rounded-lg px-4 py-2 text-sm font-medium text-white ${tones[tone]} disabled:cursor-not-allowed disabled:opacity-50 transition-colors`}
      >
        {submitting ? (
          <>
            <Spinner size="sm" /> Working...
          </>
        ) : (
          submitLabel
        )}
      </button>
    </div>
  );
}

// ── Embedded Grafana dashboard tab ─────────────────────────────────────────────
//
// Auth strategy: we iframe the URL returned by GET .../grafana-link, which points
// at the public Grafana base (GRAFANA_PUBLIC_URL from backend config). Full
// embedding (allow_embedding=true, X-Frame-Options: ALLOW-FROM) must be
// configured on the Grafana server — that is a deploy-time config concern, not a
// frontend one. For authenticated embedding (auth-proxy with X-WEBAUTH-USER, or
// per-org service-account signed embed), the backend grafana-link endpoint should
// already embed auth params in the URL it returns. We display a graceful fallback
// "Open in Grafana" link if the iframe is blocked (e.g. X-Frame-Options denies).

function GrafanaDashboardTab({
  projectId,
  envId,
  appId,
}: {
  projectId: string;
  envId: string;
  appId: string;
}) {
  const [url, setUrl] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [fetchError, setFetchError] = useState<string | null>(null);
  // iframeBlocked is set to true if the iframe fires an error event (e.g.
  // X-Frame-Options or CSP blocks it). Note: browsers don't reliably surface
  // these as JS errors; we rely on onError + a load-timeout heuristic.
  const [iframeBlocked, setIframeBlocked] = useState(false);
  const [iframeLoaded, setIframeLoaded] = useState(false);
  const loadTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (!envId) return;
    /* eslint-disable react-hooks/set-state-in-effect */
    setIsLoading(true);
    /* eslint-enable react-hooks/set-state-in-effect */
    monitoringApi
      .getGrafanaLink(projectId, envId, appId)
      .then((r) => setUrl(r.url))
      .catch((err) => setFetchError(err instanceof Error ? err.message : "Failed to get Grafana link"))
      .finally(() => setIsLoading(false));
  }, [projectId, envId, appId]);

  // Start a 10 s timeout once the URL is known. If iframe hasn't fired onLoad by
  // then we assume it was blocked (frame-busted) and show the fallback link.
  useEffect(() => {
    if (!url) return;
    loadTimeoutRef.current = setTimeout(() => {
      if (!iframeLoaded) setIframeBlocked(true);
    }, 10_000);
    return () => {
      if (loadTimeoutRef.current) clearTimeout(loadTimeoutRef.current);
    };
  }, [url, iframeLoaded]);

  if (isLoading) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner />
      </div>
    );
  }

  if (fetchError || !url) {
    return (
      <div className="space-y-4">
        <ErrorBox text={fetchError ?? "Grafana link unavailable"} />
        <p className="text-sm text-gray-500">
          The Grafana dashboard may not have been provisioned yet. Check that the monitoring app has a{" "}
          <span className="font-mono text-gray-700">grafana_dashboard_uid</span> set.
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      {/* Fallback / open-in-new link always shown above the frame */}
      <div className="flex items-center justify-between">
        <p className="text-xs text-gray-400">
          {iframeBlocked
            ? "Dashboard blocked by browser security policy — open it directly."
            : "Live Grafana dashboard. If the panel is blank, Grafana embedding may need to be enabled on the server."}
        </p>
        <a
          href={url}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-xs font-medium text-gray-700 hover:border-orange-300 hover:text-orange-600 transition-colors shadow-sm"
        >
          <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
          </svg>
          Open in Grafana
        </a>
      </div>

      {!iframeBlocked && (
        <div className="relative overflow-hidden rounded-xl border border-gray-200 bg-gray-50" style={{ height: "680px" }}>
          {!iframeLoaded && (
            <div className="absolute inset-0 flex items-center justify-center">
              <Spinner size="lg" />
            </div>
          )}
          <iframe
            src={url}
            title={`Grafana dashboard`}
            className="h-full w-full border-0"
            // allow popups for Grafana panel links, block camera/mic
            allow="clipboard-write"
            onLoad={() => {
              setIframeLoaded(true);
              if (loadTimeoutRef.current) clearTimeout(loadTimeoutRef.current);
            }}
            onError={() => setIframeBlocked(true)}
          />
        </div>
      )}
    </div>
  );
}

// ── Main page ──────────────────────────────────────────────────────────────────

export default function MonitoringDetailPage() {
  const params = useParams<{ projectId: string; appId: string }>();
  const search = useSearchParams();
  const { projectId, appId } = params;
  const { selectedEnv, role } = useProjectContext();
  const envId = search.get("envId") || selectedEnv?.id || "";

  const initialTab = (search.get("tab") as Tab) ?? "overview";
  const [tab, setTab] = useState<Tab>(initialTab);

  const [app, setApp] = useState<MonitoringApp | null>(null);
  const [health, setHealth] = useState<HealthStatus | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isGrafanaLoading, setIsGrafanaLoading] = useState(false);

  useEffect(() => {
    if (!envId) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setError("Missing envId");
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
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load monitoring app"))
      .finally(() => setIsLoading(false));
  }, [projectId, envId, appId]);

  async function openGrafana() {
    setIsGrafanaLoading(true);
    try {
      const r = await monitoringApi.getGrafanaLink(projectId, envId, appId);
      window.open(r.url, "_blank", "noopener,noreferrer");
    } catch {
      // ignore — Dashboard tab shows error inline
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
        {error ?? "Monitoring app not found"}
      </div>
    );
  }

  const tabs: { key: Tab; label: string }[] = [
    { key: "overview", label: "Overview" },
    { key: "dashboard", label: "Dashboard" },
    { key: "metrics", label: "Metrics" },
    { key: "logs", label: "Logs" },
    { key: "alerts", label: "Alerts" },
  ];

  return (
    <div>
      {/* Breadcrumb + header */}
      <div className="mb-6 flex items-start justify-between">
        <div>
          <div className="flex items-center gap-2 text-sm text-gray-500">
            <Link href="/projects" className="hover:text-gray-700">Projects</Link>
            <span>/</span>
            <Link href={`/projects/${projectId}`} className="hover:text-gray-700">Overview</Link>
            <span>/</span>
            <Link href={`/projects/${projectId}/monitoring`} className="hover:text-gray-700">Monitoring</Link>
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
          Open in Grafana
        </button>
      </div>

      {/* Tabs */}
      <div className="mb-6 border-b border-gray-200">
        <nav className="-mb-px flex gap-6">
          {tabs.map((t) => {
            const active = tab === t.key;
            return (
              <button
                key={t.key}
                onClick={() => setTab(t.key)}
                className={`border-b-2 px-1 py-3 text-sm font-medium transition-colors ${
                  active
                    ? "border-blue-600 text-blue-600"
                    : "border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-700"
                }`}
              >
                {t.label}
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
      {tab === "dashboard" && (
        <GrafanaDashboardTab
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

// ── Overview tab ───────────────────────────────────────────────────────────────

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
  return (
    <div className="space-y-6">
      {health && (
        <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
          <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">Health</h2>
          <div className="space-y-1">
            <Row label="State" value={health.state} />
            <Row label="Last seen" value={health.last_seen ? new Date(health.last_seen).toLocaleString() : "—"} />
            <Row label="Error rate (15m)" value={`${(health.error_rate_15m * 100).toFixed(2)}%`} />
            <Row label="Firing alerts" value={String(health.firing_alerts)} />
          </div>
          {health.reasons.length > 0 && (
            <div className="mt-3">
              <p className="text-xs font-medium uppercase tracking-wide text-gray-400 mb-1">Reasons</p>
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
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">Info</h2>
        <Row label="ID" value={app.id} mono />
        <Row label="Created" value={new Date(app.created_at).toLocaleString()} />
        <Row label="Updated" value={new Date(app.updated_at).toLocaleString()} />
        {app.grafana_dashboard_uid && (
          <Row label="Grafana dashboard UID" value={app.grafana_dashboard_uid} mono />
        )}
      </div>

      {/* Native SVG sparkline — kept for at-a-glance health only; rich view is the Dashboard tab */}
      <MetricsPanel kind="monitoring" projectId={projectId} envId={envId} appId={appId} />

      <LogsViewer
        projectId={projectId}
        monitoring={{ projectId, envId, appId }}
      />
    </div>
  );
}

// ── Alerts tab (unchanged from original) ──────────────────────────────────────

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
  const [rules, setRules] = useState<AlertRule[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isRuleModalOpen, setIsRuleModalOpen] = useState(false);
  const [ruleForm, setRuleForm] = useState({
    name: "",
    metric: "cpu",
    metricCustom: "",
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
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load alerts"))
      .finally(() => setIsLoading(false));
  }, [projectId, envId, appId]);

  async function deleteRule(ruleId: string) {
    try {
      await monitoringApi.deleteAlertRule(projectId, envId, appId, ruleId);
      setRules((prev) => prev.filter((r) => r.id !== ruleId));
    } catch {
      // ignore
    }
  }

  async function deleteChannel(id: string) {
    try {
      await monitoringApi.deleteChannel(projectId, envId, id);
      setChannels((prev) => prev.filter((c) => c.id !== id));
    } catch {
      // ignore
    }
  }

  async function submitRule(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setRuleError(null);
    setIsRuleSubmitting(true);
    try {
      const metric =
        ruleForm.metric === "custom" ? ruleForm.metricCustom : ruleForm.metric;
      const r = await monitoringApi.createAlertRule(projectId, envId, appId, {
        name: ruleForm.name,
        metric,
        condition: ruleForm.condition,
        threshold: ruleForm.threshold,
        duration: ruleForm.duration,
        channel_id: ruleForm.channel_id || undefined,
      });
      setRules((prev) => [...prev, r.rule]);
      setIsRuleModalOpen(false);
      setRuleForm({ name: "", metric: "cpu", metricCustom: "", condition: ">", threshold: 80, duration: "5m", channel_id: "" });
    } catch (err) {
      setRuleError(err instanceof Error ? err.message : "Failed to create rule");
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
      setChannelError(err instanceof Error ? err.message : "Failed to create channel");
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
      {/* Alert Rules */}
      <div>
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-500">Alert Rules</h2>
          {canWrite && (
            <button
              onClick={() => setIsRuleModalOpen(true)}
              className="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
            >
              <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
              </svg>
              Create Rule
            </button>
          )}
        </div>

        {rules.length === 0 ? (
          <div className="rounded-xl border border-dashed border-gray-300 bg-gray-50 py-10 text-center">
            <p className="text-sm text-gray-400">No alert rules configured.</p>
          </div>
        ) : (
          <div className="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm">
            <table className="min-w-full divide-y divide-gray-200">
              <thead className="bg-gray-50">
                <tr>
                  <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">Name</th>
                  <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">Metric</th>
                  <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">Condition</th>
                  <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">Duration</th>
                  <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">Channel</th>
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
                          Delete
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

      {/* Channels */}
      <div>
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-500">Channels</h2>
          {canWrite && (
            <button
              onClick={() => setIsChannelModalOpen(true)}
              className="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
            >
              <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
              </svg>
              Add Channel
            </button>
          )}
        </div>

        {channels.length === 0 ? (
          <div className="rounded-xl border border-dashed border-gray-300 bg-gray-50 py-10 text-center">
            <p className="text-sm text-gray-400">No notification channels configured.</p>
          </div>
        ) : (
          <div className="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm">
            <table className="min-w-full divide-y divide-gray-200">
              <thead className="bg-gray-50">
                <tr>
                  <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">Name</th>
                  <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">Type</th>
                  <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">Created</th>
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
                          Delete
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

      {/* Create Rule Modal */}
      <Modal
        isOpen={isRuleModalOpen}
        onClose={() => {
          setIsRuleModalOpen(false);
          setRuleError(null);
        }}
        title="Create Alert Rule"
      >
        <form onSubmit={submitRule} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700">Name</label>
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
              <label className="block text-sm font-medium text-gray-700">Metric</label>
              <select
                value={ruleForm.metric}
                onChange={(e) => setRuleForm((p) => ({ ...p, metric: e.target.value }))}
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              >
                <option value="cpu">cpu</option>
                <option value="memory">memory</option>
                <option value="temperature">temperature</option>
                <option value="custom">custom…</option>
              </select>
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700">Condition</label>
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

          {ruleForm.metric === "custom" && (
            <div>
              <label className="block text-sm font-medium text-gray-700">Custom metric name</label>
              <input
                type="text"
                required
                value={ruleForm.metricCustom}
                onChange={(e) => setRuleForm((p) => ({ ...p, metricCustom: e.target.value }))}
                placeholder="my_custom_metric"
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          )}

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm font-medium text-gray-700">Threshold</label>
              <input
                type="number"
                required
                value={ruleForm.threshold}
                onChange={(e) => setRuleForm((p) => ({ ...p, threshold: parseFloat(e.target.value) }))}
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700">Duration</label>
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
              Channel <span className="text-gray-400 font-normal">(optional)</span>
            </label>
            <select
              value={ruleForm.channel_id}
              onChange={(e) => setRuleForm((p) => ({ ...p, channel_id: e.target.value }))}
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="">— none —</option>
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
            submitLabel="Create Rule"
          />
        </form>
      </Modal>

      {/* Add Channel Modal */}
      <Modal
        isOpen={isChannelModalOpen}
        onClose={() => {
          setIsChannelModalOpen(false);
          setChannelError(null);
        }}
        title="Add Channel"
      >
        <form onSubmit={submitChannel} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700">Name</label>
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
            <label className="block text-sm font-medium text-gray-700">Type</label>
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
                <label className="block text-sm font-medium text-gray-700">Bot Token</label>
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
                <label className="block text-sm font-medium text-gray-700">Chat ID</label>
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
                Email addresses <span className="text-gray-400 font-normal">(comma-separated)</span>
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
              <label className="block text-sm font-medium text-gray-700">Webhook URL</label>
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
            submitLabel="Add Channel"
          />
        </form>
      </Modal>
    </div>
  );
}

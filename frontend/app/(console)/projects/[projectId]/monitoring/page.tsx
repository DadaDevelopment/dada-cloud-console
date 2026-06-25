"use client";
import { useEffect, useState, useCallback, useRef, FormEvent } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { monitoringApi } from "@/lib/api";
import type { MonitoringApp, HealthStatus } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { CopyButton } from "@/components/ui/copy-button";
import { StateChip } from "@/components/ui/state-chip";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";

// Ingest base URL for code snippets. The gateway service will be at this host.
// Change this constant (or wire it from an env var) when the gateway is deployed.
const INGEST_BASE = "https://ingest.dada-tuda.ru";

// ── Live telemetry status badge ────────────────────────────────────────────────
// Polls health every POLL_INTERVAL_MS while waiting for first data point.
// Stops polling once last_seen is non-null (receiving).
const POLL_INTERVAL_MS = 4_000;

function useTelemetryStatus(
  projectId: string,
  envId: string,
  appId: string,
  enabled: boolean
): { receiving: boolean; loading: boolean } {
  const [receiving, setReceiving] = useState(false);
  const [loading, setLoading] = useState(true);
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const poll = useCallback(async () => {
    try {
      const h: HealthStatus = await monitoringApi.getHealth(projectId, envId, appId);
      if (h.last_seen !== null) {
        setReceiving(true);
        if (timerRef.current) clearInterval(timerRef.current);
      }
    } catch {
      // health endpoint may not exist yet; stay in "waiting" state
    } finally {
      setLoading(false);
    }
  }, [projectId, envId, appId]);

  useEffect(() => {
    if (!enabled || !envId) return;
    // immediate first check
    /* eslint-disable react-hooks/set-state-in-effect */
    poll();
    /* eslint-enable react-hooks/set-state-in-effect */
    timerRef.current = setInterval(poll, POLL_INTERVAL_MS);
    return () => {
      if (timerRef.current) clearInterval(timerRef.current);
    };
  }, [enabled, envId, poll]);

  return { receiving, loading };
}

// ── Code snippets ──────────────────────────────────────────────────────────────

type SnippetTab = "nodejs" | "python" | "env" | "curl";

const SNIPPET_TABS: { key: SnippetTab; label: string }[] = [
  { key: "nodejs", label: "Node.js" },
  { key: "python", label: "Python" },
  { key: "env", label: "Env vars" },
  { key: "curl", label: "curl" },
];

function buildSnippet(tab: SnippetTab, apiKey: string): string {
  const metricsUrl = `${INGEST_BASE}/v1/metrics`;
  const logsUrl = `${INGEST_BASE}/v1/logs`;
  switch (tab) {
    case "nodejs":
      return `import { NodeSDK } from '@opentelemetry/sdk-node';
import { OTLPMetricExporter } from '@opentelemetry/exporter-metrics-otlp-http';
import { PeriodicExportingMetricReader } from '@opentelemetry/sdk-metrics';

const sdk = new NodeSDK({
  metricReader: new PeriodicExportingMetricReader({
    exporter: new OTLPMetricExporter({
      url: '${metricsUrl}',
      headers: { 'X-API-Key': '${apiKey}' },
    }),
  }),
});
sdk.start();`;
    case "python":
      return `from opentelemetry import metrics
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
from opentelemetry.exporter.otlp.proto.http.metric_exporter import OTLPMetricExporter

exporter = OTLPMetricExporter(
    endpoint="${metricsUrl}",
    headers={"X-API-Key": "${apiKey}"},
)
reader = PeriodicExportingMetricReader(exporter)
provider = MeterProvider(metric_readers=[reader])
metrics.set_meter_provider(provider)`;
    case "env":
      return `# Set these environment variables before starting your app.
# The OTel SDK will pick them up automatically (zero-code instrumentation).
OTEL_EXPORTER_OTLP_ENDPOINT=${INGEST_BASE}
OTEL_EXPORTER_OTLP_HEADERS=X-API-Key=${apiKey}
# Metrics endpoint: ${metricsUrl}
# Logs endpoint:    ${logsUrl}`;
    case "curl":
      return `# Send a test metric payload (OTLP/JSON)
curl -X POST '${metricsUrl}' \\
  -H 'Content-Type: application/json' \\
  -H 'X-API-Key: ${apiKey}' \\
  -d '{
    "resourceMetrics": [{
      "resource": {
        "attributes": [{"key":"service.name","value":{"stringValue":"my-service"}}]
      },
      "scopeMetrics": [{
        "metrics": [{
          "name": "test.counter",
          "sum": {
            "dataPoints": [{
              "asDouble": 1,
              "timeUnixNano": "${Date.now()}000000"
            }],
            "aggregationTemporality": 2,
            "isMonotonic": true
          }
        }]
      }]
    }]
  }'`;
  }
}

function CodeSnippets({ apiKey }: { apiKey: string }) {
  const [activeTab, setActiveTab] = useState<SnippetTab>("nodejs");
  const snippet = buildSnippet(activeTab, apiKey);

  return (
    <div>
      {/* Tab bar */}
      <div className="flex gap-1 border-b border-gray-200 mb-0">
        {SNIPPET_TABS.map((t) => (
          <button
            key={t.key}
            type="button"
            onClick={() => setActiveTab(t.key)}
            className={`px-3 py-1.5 text-xs font-medium rounded-t-md border-b-2 transition-colors ${
              activeTab === t.key
                ? "border-blue-600 text-blue-600 bg-white"
                : "border-transparent text-gray-500 hover:text-gray-700 hover:bg-gray-50"
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>
      {/* Code block */}
      <div className="relative rounded-b-lg rounded-tr-lg bg-gray-900 border border-t-0 border-gray-200">
        <div className="absolute top-2 right-2 z-10">
          <CopyButton value={snippet} label="Copy" className="bg-gray-700 border-gray-600 text-gray-300 hover:bg-gray-600" />
        </div>
        <pre className="overflow-x-auto px-4 py-4 text-xs leading-relaxed text-gray-200 font-mono pr-16">
          {snippet}
        </pre>
      </div>
    </div>
  );
}

// ── Onboarding card (shown inline when a resource was just created) ─────────────

function OnboardingCard({
  app,
  apiKey,
  envId,
  projectId,
}: {
  app: MonitoringApp;
  apiKey: string | null;
  envId: string;
  projectId: string;
}) {
  const { receiving, loading } = useTelemetryStatus(
    projectId,
    envId,
    app.id,
    // only poll when we have an env
    !!envId
  );

  return (
    <div className="rounded-xl border border-blue-100 bg-white shadow-sm overflow-hidden">
      {/* Status bar at top */}
      <div className="flex items-center justify-between px-5 py-3 border-b border-gray-100 bg-gray-50">
        <div className="flex items-center gap-2">
          <span className="font-mono text-sm font-semibold text-gray-900">{app.name}</span>
          <span className="text-xs text-gray-400">ready to receive telemetry</span>
        </div>
        {loading ? (
          <StateChip tone="neutral" dot>Checking...</StateChip>
        ) : receiving ? (
          <StateChip tone="ready" dot>Receiving</StateChip>
        ) : (
          <StateChip tone="needs-action" dot>Waiting for first telemetry</StateChip>
        )}
      </div>

      <div className="px-5 py-5 space-y-7">
        {/* Step 1 — done (resource created) */}
        <Step number={1} title="Create monitoring resource" done>
          <p className="text-sm text-gray-500">
            Resource <span className="font-mono text-gray-700">{app.name}</span> created.
          </p>
        </Step>

        {/* Step 2 — API key */}
        <Step number={2} title="Copy your API key" done={!apiKey}>
          {apiKey ? (
            <div className="space-y-2">
              <div className="flex items-start gap-2">
                <pre className="flex-1 overflow-x-auto rounded-lg border border-amber-200 bg-amber-50 px-3 py-2.5 font-mono text-sm text-amber-900 break-all whitespace-pre-wrap">
                  {apiKey}
                </pre>
                <CopyButton value={apiKey} label="Copy key" />
              </div>
              <p className="text-xs text-amber-700 flex items-center gap-1.5">
                <svg className="h-3.5 w-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
                </svg>
                Store this key now — it will not be shown again.
              </p>
              <p className="text-xs text-gray-400">
                Need to rotate?{" "}
                <Link
                  href={`/projects/${projectId}/monitoring/${app.id}`}
                  className="text-blue-600 hover:text-blue-700"
                >
                  Manage keys →
                </Link>
              </p>
            </div>
          ) : (
            <p className="text-sm text-gray-400">Key was already stored.</p>
          )}
        </Step>

        {/* Step 3 — Connect your device */}
        <Step number={3} title="Connect your device">
          <p className="mb-3 text-sm text-gray-500">
            Use the OTel SDK with the endpoint and key below, or set env vars for zero-code instrumentation.
          </p>
          <CodeSnippets apiKey={apiKey ?? "dmon_<your-key>"} />
        </Step>
      </div>
    </div>
  );
}

function Step({
  number,
  title,
  done = false,
  children,
}: {
  number: number;
  title: string;
  done?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="flex gap-4">
      {/* Circle number */}
      <div className="flex-none flex flex-col items-center">
        <div
          className={`h-7 w-7 rounded-full flex items-center justify-center text-xs font-bold ${
            done
              ? "bg-green-100 text-green-700"
              : "bg-blue-600 text-white"
          }`}
        >
          {done ? (
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
            </svg>
          ) : (
            number
          )}
        </div>
      </div>
      <div className="flex-1 min-w-0 pt-0.5">
        <p className="text-sm font-semibold text-gray-800 mb-2">{title}</p>
        {children}
      </div>
    </div>
  );
}

// ── Main page ──────────────────────────────────────────────────────────────────

export default function MonitoringPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;

  const { project, selectedEnv, role, loading: isLoadingEnvs } = useProjectContext();
  const selectedEnvId = selectedEnv?.id ?? "";

  const [apps, setApps] = useState<MonitoringApp[]>([]);
  const [isLoadingApps, setIsLoadingApps] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isModalOpen, setIsModalOpen] = useState(false);
  const [name, setName] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  // After create: keep the new app + plaintext key in state so the onboarding
  // card can be rendered inline (without a modal interrupt).
  const [onboardingApp, setOnboardingApp] = useState<MonitoringApp | null>(null);
  const [onboardingKey, setOnboardingKey] = useState<string | null>(null);

  useEffect(() => {
    /* eslint-disable react-hooks/set-state-in-effect */
    if (!selectedEnvId) {
      if (!isLoadingEnvs) setIsLoadingApps(false);
      return;
    }
    setIsLoadingApps(true);
    setError(null);
    /* eslint-enable react-hooks/set-state-in-effect */
    monitoringApi
      .list(projectId)
      .then((data) => setApps(data.monitoring_apps ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load monitoring apps"))
      .finally(() => setIsLoadingApps(false));
  }, [projectId, selectedEnvId, isLoadingEnvs]);

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitError(null);
    setIsSubmitting(true);
    try {
      const result = await monitoringApi.create(projectId, selectedEnvId, name);
      setApps((prev) => [...prev, result.monitoring_app]);
      setIsModalOpen(false);
      setName("");
      // Show the onboarding card inline instead of a popup modal.
      setOnboardingApp(result.monitoring_app);
      setOnboardingKey(result.api_key);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : "Failed to create monitoring app");
    } finally {
      setIsSubmitting(false);
    }
  }

  const canCreate = canMutate(role);

  if (isLoadingEnvs) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner size="lg" />
      </div>
    );
  }

  return (
    <div>
      {/* Header */}
      <div className="mb-8 flex items-start justify-between">
        <div>
          <Breadcrumb
            items={[
              { label: "Projects", href: "/projects" },
              { label: project?.display_name ?? "Overview", href: `/projects/${projectId}` },
              { label: "Monitoring" },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900">Monitoring</h1>
          <p className="mt-0.5 text-sm text-gray-500">Push telemetry from any device via OpenTelemetry — dashboards and alerts included.</p>
        </div>
        {canCreate && apps.length > 0 && (
          <button
            onClick={() => setIsModalOpen(true)}
            disabled={!selectedEnvId}
            className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            Create Monitoring
          </button>
        )}
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          {error}
        </div>
      )}

      {isLoadingApps ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : apps.length === 0 && !onboardingApp ? (
        /* ── Zero state: guided onboarding first step ── */
        <ZeroState
          canCreate={canCreate}
          hasEnv={!!selectedEnvId}
          onCreateClick={() => setIsModalOpen(true)}
        />
      ) : (
        <div className="space-y-6">
          {/* Inline onboarding card for the most recently created app */}
          {onboardingApp && (
            <div>
              <div className="mb-3 flex items-center justify-between">
                <p className="text-xs font-semibold uppercase tracking-wide text-blue-600">
                  Getting started
                </p>
                <button
                  type="button"
                  onClick={() => { setOnboardingApp(null); setOnboardingKey(null); }}
                  className="text-xs text-gray-400 hover:text-gray-600 transition-colors"
                >
                  Dismiss
                </button>
              </div>
              <OnboardingCard
                app={onboardingApp}
                apiKey={onboardingKey}
                envId={selectedEnvId}
                projectId={projectId}
              />
            </div>
          )}

          {/* App cards grid */}
          {apps.length > 0 && (
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {apps.map((app) => (
                <Link
                  key={app.id}
                  href={`/projects/${projectId}/monitoring/${app.id}${selectedEnvId ? `?envId=${selectedEnvId}` : ""}`}
                  className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm transition-all hover:border-blue-200 hover:shadow-md"
                >
                  <div className="mb-3 flex items-start justify-between">
                    <div>
                      <p className="font-mono text-sm font-semibold text-gray-900">{app.name}</p>
                      <p className="mt-0.5 text-xs text-gray-400">monitoring app</p>
                    </div>
                  </div>
                  <p className="text-xs text-gray-400">
                    Created {new Date(app.created_at).toLocaleDateString()}
                  </p>
                </Link>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Create Monitoring Modal */}
      <Modal
        isOpen={isModalOpen}
        onClose={() => {
          setIsModalOpen(false);
          setSubmitError(null);
          setName("");
        }}
        title="Create Monitoring App"
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700">Name</label>
            <input
              type="text"
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-service-monitor"
              pattern="[a-z0-9-]+"
              title="Lowercase letters, numbers, and hyphens only"
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          {submitError && (
            <div role="alert" className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
              {submitError}
            </div>
          )}

          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => {
                setIsModalOpen(false);
                setSubmitError(null);
                setName("");
              }}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100 transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={isSubmitting}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {isSubmitting ? (
                <>
                  <Spinner size="sm" />
                  Creating...
                </>
              ) : (
                "Create"
              )}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}

// ── Zero state ─────────────────────────────────────────────────────────────────

function ZeroState({
  canCreate,
  hasEnv,
  onCreateClick,
}: {
  canCreate: boolean;
  hasEnv: boolean;
  onCreateClick: () => void;
}) {
  return (
    <div className="rounded-xl border border-dashed border-gray-300 bg-gray-50 px-6 py-12">
      <div className="mx-auto max-w-md text-center">
        {/* Icon */}
        <div className="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-full bg-blue-50 border border-blue-100">
          <svg className="h-7 w-7 text-blue-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M3 13.125C3 12.504 3.504 12 4.125 12h2.25c.621 0 1.125.504 1.125 1.125v6.75C7.5 20.496 6.996 21 6.375 21h-2.25A1.125 1.125 0 013 19.875v-6.75zM9.75 8.625c0-.621.504-1.125 1.125-1.125h2.25c.621 0 1.125.504 1.125 1.125v11.25c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V8.625zM16.5 4.125c0-.621.504-1.125 1.125-1.125h2.25C20.496 3 21 3.504 21 4.125v15.75c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V4.125z" />
          </svg>
        </div>
        <h2 className="text-base font-semibold text-gray-900">Start receiving telemetry</h2>
        <p className="mt-1 text-sm text-gray-500">
          Create a monitoring resource, copy your API key, and push metrics or logs from any device using the stock OpenTelemetry SDK.
        </p>
        {canCreate && (
          <button
            type="button"
            onClick={onCreateClick}
            disabled={!hasEnv}
            className="mt-5 inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            Create Monitoring App
          </button>
        )}
        {/* Step preview */}
        <div className="mt-8 text-left space-y-3">
          {[
            { n: 1, text: "Create a monitoring resource — gets a scoped API key." },
            { n: 2, text: "Copy the key (shown once) and store it securely." },
            { n: 3, text: "Point your OTel SDK at the ingest endpoint — live dashboard in seconds." },
          ].map(({ n, text }) => (
            <div key={n} className="flex items-start gap-3">
              <span className="flex-none flex h-5 w-5 items-center justify-center rounded-full bg-gray-200 text-xs font-bold text-gray-600">
                {n}
              </span>
              <p className="text-sm text-gray-600">{text}</p>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

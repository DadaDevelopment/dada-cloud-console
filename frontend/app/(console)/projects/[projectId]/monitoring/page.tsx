"use client";
import { useEffect, useState, useCallback, useRef, FormEvent } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { monitoringApi } from "@/lib/api";
import { docsHref } from "@/lib/site";
import type { MonitoringApp, HealthStatus } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { CopyButton } from "@/components/ui/copy-button";
import { StateChip } from "@/components/ui/state-chip";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { useT } from "@/lib/i18n/console/context";

const INGEST_BASE = "https://ingest.dada-tuda.ru";

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
    } finally {
      setLoading(false);
    }
  }, [projectId, envId, appId]);

  useEffect(() => {
    if (!enabled || !envId) return;
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
import { resourceFromAttributes } from '@opentelemetry/resources';

const sdk = new NodeSDK({
  // service.instance.id is optional: it becomes a "source" label you can group and filter by.
  resource: resourceFromAttributes({
    'service.name': 'my-service',
    'service.instance.id': 'instance-01',
  }),
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
from opentelemetry.sdk.resources import Resource
from opentelemetry.exporter.otlp.proto.http.metric_exporter import OTLPMetricExporter

# service.instance.id is optional: it becomes a "source" label you can group and filter by.
resource = Resource.create({
    "service.name": "my-service",
    "service.instance.id": "instance-01",
})
exporter = OTLPMetricExporter(
    endpoint="${metricsUrl}",
    headers={"X-API-Key": "${apiKey}"},
)
reader = PeriodicExportingMetricReader(exporter)
provider = MeterProvider(resource=resource, metric_readers=[reader])
metrics.set_meter_provider(provider)`;
    case "env":
      return `# Set these environment variables before starting your app.
# The OTel SDK will pick them up automatically (zero-code instrumentation).
OTEL_EXPORTER_OTLP_ENDPOINT=${INGEST_BASE}
OTEL_EXPORTER_OTLP_HEADERS=X-API-Key=${apiKey}
# service.instance.id is optional: it becomes a "source" label you can group and filter by.
OTEL_RESOURCE_ATTRIBUTES=service.name=my-service,service.instance.id=instance-01
# Metrics endpoint: ${metricsUrl}
# Logs endpoint:    ${logsUrl}`;
    case "curl":
      return `# Send a test counter sample (OTLP/JSON). Re-run it a few times: the value
# climbs with the clock, so the console charts a real rate instead of a flat line.
# service.instance.id is optional: it becomes a "source" label you can group/filter by.
NOW=$(date +%s)
curl -X POST '${metricsUrl}' \\
  -H 'Content-Type: application/json' \\
  -H 'X-API-Key: ${apiKey}' \\
  -d '{
    "resourceMetrics": [{
      "resource": {
        "attributes": [
          {"key":"service.name","value":{"stringValue":"my-service"}},
          {"key":"service.instance.id","value":{"stringValue":"instance-01"}}
        ]
      },
      "scopeMetrics": [{
        "metrics": [{
          "name": "test.counter",
          "sum": {
            "dataPoints": [{
              "asDouble": '"$NOW"',
              "timeUnixNano": "'"$NOW"'000000000"
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
  const { t } = useT();
  const [activeTab, setActiveTab] = useState<SnippetTab>("nodejs");
  const snippet = buildSnippet(activeTab, apiKey);

  return (
    <div>
      <div className="flex gap-1 border-b border-gray-200 dark:border-gray-800 mb-0">
        {SNIPPET_TABS.map((tab) => (
          <button
            key={tab.key}
            type="button"
            onClick={() => setActiveTab(tab.key)}
            className={`px-3 py-1.5 text-xs font-medium rounded-t-md border-b-2 transition-colors ${
              activeTab === tab.key
                ? "border-blue-600 text-blue-600 bg-white dark:bg-gray-900"
                : "border-transparent text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200 hover:bg-gray-50 dark:hover:bg-gray-800"
            }`}
          >
            {tab.label}
          </button>
        ))}
      </div>
      <div className="relative rounded-b-lg rounded-tr-lg bg-gray-900 border border-t-0 border-gray-200 dark:border-gray-800">
        <div className="absolute top-2 right-2 z-10">
          <CopyButton value={snippet} label={t("common.copy")} className="bg-gray-700 border-gray-600 text-gray-300 hover:bg-gray-600" />
        </div>
        <pre className="overflow-x-auto px-4 py-4 text-xs leading-relaxed text-gray-200 font-mono pr-16">
          {snippet}
        </pre>
      </div>
    </div>
  );
}

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
  const { t } = useT();
  const { receiving, loading } = useTelemetryStatus(
    projectId,
    envId,
    app.id,
    !!envId
  );

  return (
    <div className="rounded-xl border border-blue-100 dark:border-blue-900 bg-white dark:bg-gray-900 shadow-sm overflow-hidden">
      <div className="flex items-center justify-between px-5 py-3 border-b border-gray-100 dark:border-gray-800 bg-gray-50 dark:bg-gray-900">
        <div className="flex items-center gap-2">
          <span className="font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{app.name}</span>
          <span className="text-xs text-gray-400 dark:text-gray-500">{t("monitoring.status.readyToReceive")}</span>
        </div>
        {loading ? (
          <StateChip tone="neutral" dot>{t("monitoring.status.checking")}</StateChip>
        ) : receiving ? (
          <StateChip tone="ready" dot>{t("monitoring.status.receiving")}</StateChip>
        ) : (
          <StateChip tone="needs-action" dot>{t("monitoring.status.waiting")}</StateChip>
        )}
      </div>

      <div className="px-5 py-5 space-y-7">
        <Step number={1} title={t("monitoring.step1.title")} done>
          <p className="text-sm text-gray-500">
            {t("monitoring.step1.body", { name: app.name })}
          </p>
        </Step>

        <Step number={2} title={t("monitoring.step2.title")} done={!apiKey}>
          {apiKey ? (
            <div className="space-y-2">
              <div className="flex items-start gap-2">
                <pre className="flex-1 overflow-x-auto rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-3 py-2.5 font-mono text-sm text-amber-900 dark:text-amber-100 break-all whitespace-pre-wrap">
                  {apiKey}
                </pre>
                <CopyButton value={apiKey} label={t("common.copy")} />
              </div>
              <p className="text-xs text-amber-700 dark:text-amber-300 flex items-center gap-1.5">
                <svg className="h-3.5 w-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
                </svg>
                {t("monitoring.step2.warning")}
              </p>
              <p className="text-xs text-gray-400 dark:text-gray-500">
                {t("monitoring.step2.needRotate")}{" "}
                <Link
                  href={`/projects/${projectId}/monitoring/${app.id}`}
                  className="text-blue-600 hover:text-blue-700"
                >
                  {t("monitoring.step2.manageKeys")}
                </Link>
              </p>
            </div>
          ) : (
            <p className="text-sm text-gray-400">{t("monitoring.step2.keyStored")}</p>
          )}
        </Step>

        <Step number={3} title={t("monitoring.step3.title")}>
          <p className="mb-3 text-sm text-gray-500">
            {t("monitoring.step3.body")}
          </p>
          <details className="group rounded-lg border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900/60">
            <summary className="cursor-pointer select-none px-4 py-2.5 text-sm font-medium text-gray-700 dark:text-gray-200 marker:text-gray-400">
              {t("monitoring.step3.advanced")}
            </summary>
            <div className="border-t border-gray-200 dark:border-gray-800 px-4 py-4">
              <p className="mb-3 text-xs text-gray-500 dark:text-gray-400">{t("monitoring.step3.advancedHint")}</p>
              <CodeSnippets apiKey={apiKey ?? "dmon_<your-key>"} />
            </div>
          </details>
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
      <div className="flex-none flex flex-col items-center">
        <div
          className={`h-7 w-7 rounded-full flex items-center justify-center text-xs font-bold ${
            done
              ? "bg-green-100 dark:bg-green-950/40 text-green-700 dark:text-green-300"
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
        <p className="text-sm font-semibold text-gray-800 dark:text-gray-200 mb-2">{title}</p>
        {children}
      </div>
    </div>
  );
}

export default function MonitoringPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const { t } = useT();

  const { project, selectedEnv, role, loading: isLoadingEnvs } = useProjectContext();
  const selectedEnvId = selectedEnv?.id ?? "";

  const [apps, setApps] = useState<MonitoringApp[]>([]);
  const [isLoadingApps, setIsLoadingApps] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isModalOpen, setIsModalOpen] = useState(false);
  const [name, setName] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

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
      .catch((err) => setError(err instanceof Error ? err.message : t("monitoring.error.load")))
      .finally(() => setIsLoadingApps(false));
  // eslint-disable-next-line react-hooks/exhaustive-deps
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
      setOnboardingApp(result.monitoring_app);
      setOnboardingKey(result.api_key);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : t("monitoring.error.create"));
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
      <div className="mb-8 flex flex-wrap items-start justify-between gap-3">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.projects"), href: "/projects" },
              { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
              { label: t("nav.monitoring") },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("monitoring.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("monitoring.subtitle")}</p>
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
            {t("monitoring.create")}
          </button>
        )}
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
          {error}
        </div>
      )}

      {isLoadingApps ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : apps.length === 0 && !onboardingApp ? (
        <ZeroState
          canCreate={canCreate}
          hasEnv={!!selectedEnvId}
          onCreateClick={() => setIsModalOpen(true)}
        />
      ) : (
        <div className="space-y-6">
          {onboardingApp && (
            <div>
              <div className="mb-3 flex items-center justify-between">
                <p className="text-xs font-semibold uppercase tracking-wide text-blue-600">
                  {t("monitoring.onboarding.label")}
                </p>
                <button
                  type="button"
                  onClick={() => { setOnboardingApp(null); setOnboardingKey(null); }}
                  className="text-xs text-gray-400 hover:text-gray-600 transition-colors"
                >
                  {t("monitoring.dismiss")}
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

          {apps.length > 0 && (
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {apps.map((app) => (
                <Link
                  key={app.id}
                  href={`/projects/${projectId}/monitoring/${app.id}${selectedEnvId ? `?envId=${selectedEnvId}` : ""}`}
                  className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm transition-all hover:border-blue-200 hover:shadow-md"
                >
                  <div className="mb-3 flex items-start justify-between">
                    <div>
                      <p className="font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{app.name}</p>
                      <p className="mt-0.5 text-xs text-gray-400 dark:text-gray-500">{t("monitoring.card.monitoringApp")}</p>
                    </div>
                  </div>
                  <p className="text-xs text-gray-400 dark:text-gray-500">
                    {t("monitoring.card.createdAt", { date: new Date(app.created_at).toLocaleDateString() })}
                  </p>
                </Link>
              ))}
            </div>
          )}
        </div>
      )}

      <Modal
        isOpen={isModalOpen}
        onClose={() => {
          setIsModalOpen(false);
          setSubmitError(null);
          setName("");
        }}
        title={t("monitoring.modal.title")}
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("monitoring.modal.name.label")}</label>
            <input
              type="text"
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t("monitoring.modal.name.placeholder")}
              pattern="[a-z0-9-]+"
              title={t("monitoring.modal.name.validation")}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          {submitError && (
            <div role="alert" className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
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
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
            >
              {t("common.cancel")}
            </button>
            <button
              type="submit"
              disabled={isSubmitting}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {isSubmitting ? (
                <>
                  <Spinner size="sm" />
                  {t("common.creating")}
                </>
              ) : (
                t("common.create")
              )}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}

function ZeroState({
  canCreate,
  hasEnv,
  onCreateClick,
}: {
  canCreate: boolean;
  hasEnv: boolean;
  onCreateClick: () => void;
}) {
  const { t } = useT();

  return (
    <div className="rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 px-6 py-12">
      <div className="mx-auto max-w-md text-center">
        <div className="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-full bg-blue-50 dark:bg-blue-950/40 border border-blue-100 dark:border-blue-900">
          <svg className="h-7 w-7 text-blue-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M3 13.125C3 12.504 3.504 12 4.125 12h2.25c.621 0 1.125.504 1.125 1.125v6.75C7.5 20.496 6.996 21 6.375 21h-2.25A1.125 1.125 0 013 19.875v-6.75zM9.75 8.625c0-.621.504-1.125 1.125-1.125h2.25c.621 0 1.125.504 1.125 1.125v11.25c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V8.625zM16.5 4.125c0-.621.504-1.125 1.125-1.125h2.25C20.496 3 21 3.504 21 4.125v15.75c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V4.125z" />
          </svg>
        </div>
        <h2 className="text-base font-semibold text-gray-900 dark:text-gray-100">{t("monitoring.zero.title")}</h2>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          {t("monitoring.zero.body")}
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
            {t("monitoring.zero.createBtn")}
          </button>
        )}
        <div className="mt-3">
          <a
            href={docsHref("monitoring-metrics-logs-alerts")}
            target="_blank"
            rel="noopener noreferrer"
            className="text-sm font-medium text-blue-600 hover:text-blue-700"
          >
            {t("common.learnMore")} →
          </a>
        </div>
        <div className="mt-8 text-left space-y-3">
          {[
            { n: 1, key: "monitoring.zero.step1" },
            { n: 2, key: "monitoring.zero.step2" },
            { n: 3, key: "monitoring.zero.step3" },
          ].map(({ n, key }) => (
            <div key={n} className="flex items-start gap-3">
              <span className="flex-none flex h-5 w-5 items-center justify-center rounded-full bg-gray-200 dark:bg-gray-700 text-xs font-bold text-gray-600 dark:text-gray-400">
                {n}
              </span>
              <p className="text-sm text-gray-600 dark:text-gray-400">{t(key)}</p>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

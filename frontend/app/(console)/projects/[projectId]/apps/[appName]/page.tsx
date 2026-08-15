"use client";
import { useEffect, useRef, useState, FormEvent } from "react";
import { useParams, useSearchParams, useRouter } from "next/navigation";
import Link from "next/link";
import { appsApi, endpointsApi, envVarsApi, customDomainsApi, deploymentsApi } from "@/lib/api";
import type { ResourceSnapshot, AppVolume, OperationResponse, DomainHostname, Deployment } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { Tooltip } from "@/components/ui/tooltip";
import { useProjectContext } from "@/lib/project-context";
import { canEditYaml, canMutate, canSeeTechnical } from "@/lib/rbac";
import { ComposeStatePanel } from "@/components/compose-state-panel";
import { FixedMetricsDashboard } from "@/components/metrics/fixed-metrics-dashboard";
import { LogsViewer } from "@/components/logs-viewer";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { CloudTaskPanel } from "@/components/cloud-task/cloud-task-panel";
import { DeployHooksCard } from "@/components/deploy/deploy-hooks-card";
import { DeployBadgeCard } from "@/components/deploy/deploy-badge-card";
import { AppPreviewPane } from "@/components/app-preview-pane";
import { AppAlertsBanner } from "@/components/deploy/app-alerts-banner";
import { getAppAlerts } from "@/lib/app-alerts";
import { normalizeAppUrlStatus, appUrlReasonMessageKey } from "@/lib/app-url-status";
import { AppNextStepCard } from "@/components/deploy/app-next-step-card";
import { AppLiveBanner } from "@/components/deploy/app-live-banner";
import { AppLastMileBanner } from "@/components/deploy/app-last-mile-banner";
import { AppDeployDriftBanner } from "@/components/deploy/app-deploy-drift-banner";
import { AppLatestBuildCard } from "@/components/deploy/app-latest-build-card";
import { getAppNextSteps } from "@/lib/app-next-step";
import { useT } from "@/lib/i18n/console/context";
import { GOAL_DEPLOY_SUCCESS, reachGoal } from "@/lib/metrika";
import { Globe, Database, AlertTriangle } from "lucide-react";
import { classifyVMResource } from "@/lib/vm-resources";
import { IngressDetail } from "@/components/resources/ingress-detail";
import { ServiceDatabaseDetail } from "@/components/resources/service-database-detail";
import { DeleteImpactModal, deleteImpactTargetKey, type DeleteImpactTarget } from "@/components/resources/delete-impact-modal";
import { MoveAppModal } from "@/components/resources/move-app-modal";

interface DomainForm {
  fqdn: string;
  auth_scheme: string;
  auth_scopes: string;
  swagger_enabled: boolean;
  swagger_path: string;
  swagger_title: string;
}

const defaultDomainForm = (appName: string): DomainForm => ({
  fqdn: "",
  auth_scheme: "none",
  auth_scopes: "",
  swagger_enabled: false,
  swagger_path: "/v3/api-docs",
  swagger_title: appName,
});

export default function AppDetailPage() {
  const params = useParams<{ projectId: string; appName: string }>();
  const searchParams = useSearchParams();
  const router = useRouter();
  const { projectId, appName } = params;
  const { role, selectedEnv } = useProjectContext();
  const { t } = useT();
  const envId = searchParams.get("envId") || selectedEnv?.id || "";

  const [app, setApp] = useState<ResourceSnapshot | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [endpoints, setEndpoints] = useState<ResourceSnapshot[]>([]);
  const [isLoadingEndpoints, setIsLoadingEndpoints] = useState(true);
  const [hostnames, setHostnames] = useState<DomainHostname[]>([]);
  const [isLoadingHostnames, setIsLoadingHostnames] = useState(true);
  const [envCount, setEnvCount] = useState<number | null>(null);
  const [deployments, setDeployments] = useState<Deployment[]>([]);

  const [isImageModalOpen, setIsImageModalOpen] = useState(false);
  const [newImage, setNewImage] = useState("");
  const [isImageSubmitting, setIsImageSubmitting] = useState(false);
  const [imageSubmitError, setImageSubmitError] = useState<string | null>(null);

  const [isDomainModalOpen, setIsDomainModalOpen] = useState(false);
  const [domainForm, setDomainForm] = useState<DomainForm>(defaultDomainForm(appName));
  const [isDomainSubmitting, setIsDomainSubmitting] = useState(false);
  const [domainSubmitError, setDomainSubmitError] = useState<string | null>(null);

  const [deleteTarget, setDeleteTarget] = useState<DeleteImpactTarget | null>(null);
  const [isMoveModalOpen, setIsMoveModalOpen] = useState(false);

  const [sourceDownloadBusy, setSourceDownloadBusy] = useState(false);
  const [sourceDownloadError, setSourceDownloadError] = useState<string | null>(null);

  const deployGoalFiredRef = useRef(false);

  useEffect(() => {
    if (!envId) return;

    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const TERMINAL = new Set(["ready", "healthy", "running", "synced", "notdeployed", "failed", "degraded", "error"]);
    const loadApp = (attempt: number) => {
      appsApi
        .list(projectId, envId)
        .then((data) => {
          if (cancelled) return;
          const found = (data.apps ?? []).find((a) => a.name === appName);
          if (found) {
            setApp(found);
            setError(null);
            setIsLoading(false);
            const phase = (found.phase ?? "").toLowerCase();
            const SUCCESS = new Set(["ready", "healthy", "running"]);
            if (!deployGoalFiredRef.current && SUCCESS.has(phase)) {
              const deployGoalKey = `dada_deploy_goal:${projectId}:${appName}`;
              let alreadyFired = false;
              try {
                alreadyFired = typeof window !== "undefined" && window.localStorage.getItem(deployGoalKey) === "1";
              } catch {
                alreadyFired = false;
              }
              deployGoalFiredRef.current = true;
              if (!alreadyFired) {
                try {
                  window.localStorage.setItem(deployGoalKey, "1");
                } catch {}
                reachGoal(GOAL_DEPLOY_SUCCESS);
              }
            }
            const settled = TERMINAL.has(phase);
            if (!settled && attempt < 40) {
              timer = setTimeout(() => loadApp(attempt + 1), 3000);
            }
          } else if (attempt < 40) {
            timer = setTimeout(() => loadApp(attempt + 1), 3000);
          } else {
            setError(t("apps.detail.error.notFound"));
            setIsLoading(false);
          }
        })
        .catch((err) => {
          if (cancelled) return;
          setError(err instanceof Error ? err.message : t("apps.detail.error.load"));
          setIsLoading(false);
        });
    };
    loadApp(0);

    endpointsApi
      .list(projectId, envId, appName)
      .then((data) => setEndpoints(data.endpoints ?? []))
      .catch(() => setEndpoints([]))
      .finally(() => setIsLoadingEndpoints(false));

    customDomainsApi
      .listHostnames(projectId, envId, appName)
      .then((data) => setHostnames(data.hostnames ?? []))
      .catch(() => setHostnames([]))
      .finally(() => setIsLoadingHostnames(false));

    envVarsApi
      .list(projectId, envId, appName)
      .then((data) => setEnvCount((data.env_vars ?? []).length))
      .catch(() => setEnvCount(null));

    deploymentsApi
      .list(projectId, envId, appName)
      .then((data) => setDeployments(data.deployments ?? []))
      .catch(() => setDeployments([]));

    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, appName, envId]);

  async function handleImageUpdate(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setImageSubmitError(null);
    setIsImageSubmitting(true);
    try {
      const result = await appsApi.updateImage(projectId, envId, appName, newImage);
      setIsImageModalOpen(false);
      setNewImage("");
      const opId = result.operation?.id;
      router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
    } catch (err) {
      setImageSubmitError(err instanceof Error ? err.message : t("apps.error.updateImage"));
    } finally {
      setIsImageSubmitting(false);
    }
  }

  async function handleRollback() {
    if (!window.confirm(t("apps.rollback.confirm"))) return;
    try {
      const result = await appsApi.rollback(projectId, envId, appName);
      const opId = result.operation?.id;
      router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
    } catch (err) {
      window.alert(err instanceof Error ? err.message : t("apps.rollback.error"));
    }
  }

  async function handleRestart() {
    if (!window.confirm(t("apps.restart.confirm"))) return;
    try {
      const result = await appsApi.restart(projectId, envId, appName);
      const opId = result.operation?.id;
      router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
    } catch (err) {
      window.alert(err instanceof Error ? err.message : t("apps.restart.error"));
    }
  }

  async function handleAdopt() {
    if (!window.confirm(t("apps.adopt.confirm"))) return;
    try {
      const result = await appsApi.adopt(projectId, envId, appName);
      const opId = result.operation?.id;
      router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
    } catch (err) {
      window.alert(err instanceof Error ? err.message : t("apps.adopt.error"));
    }
  }

  function handleAppDeleted(result: OperationResponse) {
    setDeleteTarget(null);
    const opId = result.operation?.id;
    router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
  }

  function handleAppMoved(result: OperationResponse) {
    setIsMoveModalOpen(false);
    const opId = result.operation?.id;
    router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
  }

  async function handleDomainCreate(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setDomainSubmitError(null);
    setIsDomainSubmitting(true);
    try {
      const scopes = domainForm.auth_scopes
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      const result = await endpointsApi.create(projectId, envId, appName, {
        fqdn: domainForm.fqdn,
        auth_enabled: domainForm.auth_scheme !== "none",
        auth_scheme: domainForm.auth_scheme,
        auth_scopes: scopes,
        swagger_enabled: domainForm.swagger_enabled,
        swagger_path: domainForm.swagger_path || "/v3/api-docs",
        swagger_title: domainForm.swagger_title || appName,
      });
      setIsDomainModalOpen(false);
      setDomainForm(defaultDomainForm(appName));
      const opId = result.operation?.id;
      router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
    } catch (err) {
      setDomainSubmitError(err instanceof Error ? err.message : t("apps.error.createDomain"));
    } finally {
      setIsDomainSubmitting(false);
    }
  }

  if (isLoading) {
    return <div className="flex h-64 items-center justify-center"><Spinner size="lg" /></div>;
  }
  if (error || !app) {
    return (
      <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
        {error ?? t("apps.detail.error.notFound")}
      </div>
    );
  }

  const summary = app.summary_json as { image?: string; port?: number; replicas?: number; ready?: number; restarts?: number; profile?: string; resources?: { cpu_limit: string; memory_limit: string }; observed_resources?: { cpu_request?: string; cpu_limit?: string; memory_request?: string; memory_limit?: string }; runtime?: string; volume?: AppVolume; repo_full_name?: string; source?: string; url?: string; url_status?: string; url_reason?: string; preview_url?: string; git_sha?: string; git_message?: string; http_status?: number; http_reason?: string; http_checked_at?: string };
  const urlStatus = normalizeAppUrlStatus(summary.url_status);
  const urlReasonKey = appUrlReasonMessageKey(summary.url_reason);
  const urlReason = summary.url_reason ? (urlReasonKey ? t(urlReasonKey) : t("apps.url.reason.unknown", { reason: summary.url_reason })) : null;
  const isUploadedSource = summary.source === "archive";
  const isCompose = summary.runtime === "compose";
  const resType = classifyVMResource(app);
  const isResource = resType !== "app";
  const alerts = getAppAlerts(app);
  const appPhaseReady = (app.phase ?? "").toLowerCase() === "ready";
  const isReadyNoAlerts = appPhaseReady && alerts.length === 0;
  const observedCpu = summary.observed_resources?.cpu_limit ?? summary.observed_resources?.cpu_request;
  const observedMem = summary.observed_resources?.memory_limit ?? summary.observed_resources?.memory_request;
  const observedSize = observedCpu && observedMem ? `${observedCpu} CPU · ${observedMem}` : null;
  const sizeValue = summary.resources
    ? `${summary.resources.cpu_limit} CPU · ${summary.resources.memory_limit}`
    : observedSize
      ? (summary.replicas ?? 1) > 1
        ? t("apps.detail.spec.sizeTotal", { value: observedSize })
        : observedSize
      : "—";
  const replicasValue =
    summary.replicas == null
      ? "—"
      : summary.ready == null
        ? String(summary.replicas)
        : t("apps.detail.spec.replicasReady", { ready: summary.ready, desired: summary.replicas });
  const replicasTip = summary.restarts ? t("apps.detail.spec.restarts", { n: summary.restarts }) : undefined;
  const gitSha = summary.git_sha?.slice(0, 7);
  const gitValue = summary.repo_full_name ?? (gitSha ? t("apps.detail.config.gitCommit", { sha: gitSha }) : t("apps.detail.config.gitNone"));
  const hasGitSource = !!summary.repo_full_name || !!summary.git_sha;
  /**
   * Endpoints minus the ones already listed as hostnames. Attaching a default
   * domain writes both a domain_hostnames row and a surrogate PublicApi for the
   * same FQDN, so the tab printed every default domain twice — once as "Домен по
   * умолчанию" and once as "auth: none" — with two independently computed
   * badges that can disagree. The hostname row wins: its status is the one
   * backed by a route + certificate check (RevalidateActiveHostnameRoutes).
   */
  const extraEndpoints = endpoints.filter((ep) => {
    const s = ep.summary_json as { fqdn?: string; spec?: { dns?: { fqdn?: string } } };
    const fqdn = s.spec?.dns?.fqdn ?? s.fqdn;
    return !fqdn || !hostnames.some((h) => h.hostname === fqdn);
  });

  const nextSteps =
    !isResource && isReadyNoAlerts && !isLoadingHostnames
      ? getAppNextSteps({
          hasCustomDomain: hostnames.some((h) => !h.managed),
          hasGitRepo: hasGitSource,
        })
      : [];

  async function downloadSource() {
    setSourceDownloadBusy(true);
    setSourceDownloadError(null);
    try {
      const d = await appsApi.downloadSourceArchive(projectId, envId, appName);
      window.location.href = d.url;
    } catch (e) {
      setSourceDownloadError(e instanceof Error ? e.message : t("apps.settings.source.error"));
    } finally {
      setSourceDownloadBusy(false);
    }
  }

  return (
    <div>
      <div className="mb-8 flex flex-wrap items-start justify-between gap-3">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.projects"), href: "/projects" },
              { label: t("common.crumb.overview"), href: `/projects/${projectId}` },
              { label: t("nav.apps"), href: `/projects/${projectId}/apps` },
              { label: appName },
            ]}
          />
          <div className="mt-2 flex items-center gap-3">
            <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100 font-mono">{appName}</h1>
            <PhaseBadge phase={app.phase} />
            {resType === "ingress" && (
              <span className="inline-flex items-center gap-1.5 rounded-full bg-blue-50 dark:bg-blue-950/40 px-3 py-1 text-sm font-medium text-blue-700 dark:text-blue-300 ring-1 ring-inset ring-blue-200 dark:ring-blue-900">
                <Globe className="h-4 w-4" /> {t("resources.type.ingress")}
              </span>
            )}
            {resType === "database" && (
              <span className="inline-flex items-center gap-1.5 rounded-full bg-violet-50 dark:bg-violet-950/40 px-3 py-1 text-sm font-medium text-violet-700 dark:text-violet-300 ring-1 ring-inset ring-violet-200 dark:ring-violet-900">
                <Database className="h-4 w-4" /> {t("resources.type.database")}
              </span>
            )}
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Link
            href={`/projects/${projectId}/apps/${appName}/deployments${envId ? `?envId=${envId}` : ""}`}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 16.5V9.75m0 0l3 3m-3-3l-3 3M6.75 19.5a4.5 4.5 0 01-1.41-8.775 5.25 5.25 0 0110.233-2.33 3 3 0 013.758 3.848A3.752 3.752 0 0118 19.5H6.75z" />
            </svg>
            {t("apps.detail.deployments")}
          </Link>
          {isCompose && canMutate(role) && (
            <>
              <button
                onClick={handleRestart}
                className="inline-flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
              >
                <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0l3.181 3.183a8.25 8.25 0 0013.803-3.7M4.031 9.865a8.25 8.25 0 0113.803-3.7l3.181 3.182m0-4.991v4.99" />
                </svg>
                {t("apps.restart.button")}
              </button>
              <button
                onClick={handleRollback}
                className="inline-flex items-center gap-2 rounded-lg border border-amber-300 dark:border-amber-800 bg-white dark:bg-gray-900 px-3 py-2 text-sm font-medium text-amber-700 dark:text-amber-300 hover:bg-amber-50 dark:hover:bg-amber-950/30 transition-colors shadow-sm"
              >
                <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 15L3 9m0 0l6-6M3 9h12a6 6 0 010 12h-3" />
                </svg>
                {t("apps.rollback.button")}
              </button>
              {!isResource && (
                <button
                  onClick={handleAdopt}
                  title={t("apps.adopt.hint")}
                  className="inline-flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
                >
                  <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 6h16M4 12h16M4 18h7" />
                  </svg>
                  {t("apps.adopt.button")}
                </button>
              )}
            </>
          )}
          <Link
            href={`/projects/${projectId}/apps/${appName}/settings${envId ? `?envId=${envId}` : ""}`}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10.343 3.94c.09-.542.56-.94 1.11-.94h1.093c.55 0 1.02.398 1.11.94l.149.894c.07.424.384.764.78.93.398.164.855.142 1.205-.108l.737-.527a1.125 1.125 0 011.45.12l.773.774c.39.389.44 1.002.12 1.45l-.527.737c-.25.35-.272.806-.107 1.204.165.397.505.71.93.78l.893.15c.543.09.94.56.94 1.109v1.094c0 .55-.397 1.02-.94 1.11l-.893.149c-.425.07-.765.383-.93.78-.165.398-.143.854.107 1.204l.527.738c.32.447.269 1.06-.12 1.45l-.774.773a1.125 1.125 0 01-1.449.12l-.738-.527c-.35-.25-.806-.272-1.203-.107-.397.165-.71.505-.781.929l-.149.894c-.09.542-.56.94-1.11.94h-1.094c-.55 0-1.019-.398-1.11-.94l-.148-.894c-.071-.424-.384-.764-.781-.93-.398-.164-.854-.142-1.204.108l-.738.527c-.447.32-1.06.269-1.45-.12l-.773-.774a1.125 1.125 0 01-.12-1.45l.527-.737c.25-.35.273-.806.108-1.204-.165-.397-.505-.71-.93-.78l-.894-.15c-.542-.09-.94-.56-.94-1.109v-1.094c0-.55.398-1.02.94-1.11l.894-.149c.424-.07.765-.383.93-.78.165-.398.143-.854-.107-1.204l-.527-.738a1.125 1.125 0 01.12-1.45l.773-.773a1.125 1.125 0 011.45-.12l.737.527c.35.25.807.272 1.204.107.397-.165.71-.505.78-.929l.15-.894z" />
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
            </svg>
            {t("apps.detail.settings")}
          </Link>
          {summary.volume?.path && (
            <Link
              href={`/projects/${projectId}/apps/${appName}/files${envId ? `?envId=${envId}` : ""}`}
              className="inline-flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
            >
              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" />
              </svg>
              {t("apps.files.title")}
            </Link>
          )}
          {canEditYaml(role) && !isCompose && (
          <Link
            href={`/projects/${projectId}/apps/${appName}/values${envId ? `?envId=${envId}` : ""}`}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
            </svg>
            {t("apps.detail.editValues")}
          </Link>
          )}
          {canMutate(role) && !isResource && (
            <button
              onClick={() => { setNewImage(summary.image ?? ""); setIsImageModalOpen(true); }}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-5 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 active:bg-blue-800 transition-colors"
            >
              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12" />
              </svg>
              {t("apps.detail.deployImage")}
            </button>
          )}
        </div>
      </div>

      <AppAlertsBanner
        alerts={alerts}
        logsHref={`/projects/${projectId}/apps/${appName}${envId ? `?envId=${envId}` : ""}#logs`}
        storageHref={`/projects/${projectId}/apps/${appName}/settings?tab=storage${envId ? `&envId=${envId}` : ""}`}
        startCommandHref={`/projects/${projectId}/apps/${appName}/settings?tab=config${envId ? `&envId=${envId}` : ""}#start-command`}
        projectId={projectId}
        envId={envId}
        appName={appName}
      />

      {!isResource && (
        <AppLastMileBanner
          summary={{ http_status: summary.http_status, http_reason: summary.http_reason, http_checked_at: summary.http_checked_at }}
        />
      )}

      {!isResource && (
        <AppDeployDriftBanner
          deployments={deployments}
          deploymentsHref={`/projects/${projectId}/apps/${appName}/deployments${envId ? `?envId=${envId}` : ""}`}
        />
      )}

      {!isResource && (
        <AppLiveBanner
          projectId={projectId}
          appName={appName}
          url={summary.url}
          phase={app.phase}
          urlStatus={urlStatus}
          urlReason={summary.url_reason}
        />
      )}

      {!isResource && (
        <AppLatestBuildCard
          projectId={projectId}
          envId={envId}
          appName={appName}
          appUrl={summary.url}
          appUrlStatus={urlStatus}
          appUrlReason={urlReason}
          appReady={appPhaseReady}
          hasGitRepo={!!summary.repo_full_name}
          buildHref={(buildId) => `/projects/${projectId}/apps/${appName}/builds/${buildId}${envId ? `?envId=${envId}` : ""}`}
        />
      )}

      {!isResource && isUploadedSource && (
        <div className="mb-6 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-4 shadow-sm">
          <div className="min-w-0">
            <p className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("apps.settings.source.title")}</p>
            <p className="mt-0.5 truncate text-xs text-gray-500 dark:text-gray-400">{t("apps.settings.source.subtitle")}</p>
          </div>
          <div className="flex shrink-0 flex-col items-end gap-1">
            <button
              type="button"
              onClick={downloadSource}
              disabled={sourceDownloadBusy}
              data-ux="app_source_download:click"
              className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-xs font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50 transition-colors"
            >
              {sourceDownloadBusy ? t("apps.settings.source.busy") : t("apps.settings.source.download")}
            </button>
            {sourceDownloadError && (
              <p className="text-xs text-red-600 dark:text-red-400">{sourceDownloadError}</p>
            )}
          </div>
        </div>
      )}

      <AppNextStepCard
        steps={nextSteps}
        onConnectDomain={() => { setDomainForm(defaultDomainForm(appName)); setIsDomainModalOpen(true); }}
        gitSettingsHref={`/projects/${projectId}/apps/${appName}/settings?tab=git${envId ? `&envId=${envId}` : ""}`}
        deploymentsHref={`/projects/${projectId}/apps/${appName}/deployments${envId ? `?envId=${envId}` : ""}`}
      />

      {!isResource && summary.url && (urlStatus === "active" || urlStatus === "unknown") && (
        <div className="mb-6">
          <AppPreviewPane
            url={summary.preview_url ?? summary.url}
            openUrl={summary.url}
            detailsUrl={`/projects/${projectId}/apps/${appName}/settings${envId ? `?envId=${envId}` : ""}`}
          />
        </div>
      )}

      {!isResource && summary.url && (urlStatus === "pending" || urlStatus === "failed") && (
        <div
          className={
            urlStatus === "failed"
              ? "mb-6 rounded-lg border border-amber-300 dark:border-amber-800 bg-amber-50 dark:bg-amber-950/20 p-4"
              : "mb-6 rounded-lg border border-dashed border-gray-200 dark:border-gray-800 p-4"
          }
        >
          <p
            className={
              urlStatus === "failed"
                ? "flex items-center gap-1.5 text-sm font-medium text-amber-700 dark:text-amber-300"
                : "text-sm font-medium text-gray-500 dark:text-gray-400"
            }
          >
            {urlStatus === "failed" && <AlertTriangle className="h-4 w-4 shrink-0" />}
            {t(urlStatus === "failed" ? "apps.url.failed.label" : "apps.url.pending.label")}
          </p>
          <p className="mt-1 font-mono text-xs text-gray-500 dark:text-gray-400">{summary.url}</p>
          {urlReason && <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{urlReason}</p>}
        </div>
      )}

      {isCompose ? (
        <div className="space-y-6">
          {resType === "ingress" && <IngressDetail app={app} />}
          {resType === "database" && <ServiceDatabaseDetail app={app} />}
          <ComposeStatePanel projectId={projectId} envId={envId} appName={appName} />
        </div>
      ) : (
        <div className="space-y-6">
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            {[
              ...(canSeeTechnical(role) ? [{ label: t("apps.detail.spec.image"), value: summary.image ?? "—", mono: true, tip: summary.image }] : []),
              { label: t("apps.detail.spec.size"), value: sizeValue },
              { label: t("apps.detail.spec.replicas"), value: replicasValue, tip: replicasTip },
              { label: t("apps.detail.spec.port"), value: summary.port != null ? String(summary.port) : "—" },
            ].map(({ label, value, mono, tip }: { label: string; value: string; mono?: boolean; tip?: string }) => (
              <div key={label} className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
                <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{label}</p>
                {tip && tip.length > 0 ? (
                  <Tooltip label={tip} className="mt-1 max-w-full">
                    <span className={`block truncate text-sm font-medium text-gray-900 dark:text-gray-100 ${mono ? "font-mono" : ""}`}>{value}</span>
                  </Tooltip>
                ) : (
                  <p className={`mt-1 text-sm font-medium text-gray-900 dark:text-gray-100 truncate ${mono ? "font-mono" : ""}`}>{value}</p>
                )}
              </div>
            ))}
          </div>

          {!isResource && (
            <div>
              <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
                {t("apps.detail.config.title")}
              </h2>
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                {[
                  {
                    label: t("apps.detail.config.env"),
                    value: envCount != null ? t("apps.detail.config.envCount", { n: envCount }) : "—",
                    set: envCount != null && envCount > 0,
                    tab: "env",
                  },
                  {
                    label: t("apps.detail.config.storage"),
                    value: summary.volume?.path
                      ? `${summary.volume.path} · ${summary.volume.size}`
                      : t("apps.detail.config.storageNone"),
                    set: !!summary.volume?.path,
                    tab: "storage",
                  },
                  {
                    label: t("apps.detail.config.git"),
                    value: gitValue,
                    set: hasGitSource,
                    tab: "git",
                  },
                ].map(({ label, value, set, tab }) => (
                  <Link
                    key={tab}
                    href={`/projects/${projectId}/apps/${appName}/settings?tab=${tab}${envId ? `&envId=${envId}` : ""}`}
                    className="group flex flex-col rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm transition-colors hover:border-blue-300 dark:hover:border-blue-700"
                  >
                    <div className="flex items-center justify-between">
                      <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{label}</p>
                      <svg className="h-4 w-4 text-gray-300 dark:text-gray-600 group-hover:text-blue-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
                      </svg>
                    </div>
                    <p className={`mt-1 truncate text-sm font-medium ${set ? "text-gray-900 dark:text-gray-100 font-mono" : "text-gray-400 dark:text-gray-500"}`}>
                      {value}
                    </p>
                  </Link>
                ))}
              </div>
            </div>
          )}

        </div>
      )}

      {!isResource && (
      <div id="agent" className="mt-10 scroll-mt-20">
        <CloudTaskPanel
          projectId={projectId}
          envId={envId ?? ""}
          appName={appName}
          appKind={isCompose ? "compose" : "web"}
          canMutate={canMutate(role)}
        />
      </div>
      )}

      {!isCompose && app.phase === "NotDeployed" ? (
        <div className="mt-10 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-4 py-6 text-center text-sm text-gray-500 dark:text-gray-400">
          {t("apps.detail.observability.notDeployed")}
        </div>
      ) : (
        <div id="logs" className="mt-10 scroll-mt-20 space-y-6">
          <FixedMetricsDashboard kind="app" projectId={projectId} envId={envId} appName={appName} />
          <LogsViewer projectId={projectId} app={appName} />
        </div>
      )}

      {!isResource && (
      <div className="mt-10">
        <DeployHooksCard
          projectId={projectId}
          envId={envId ?? ""}
          appName={appName}
          canMutate={canMutate(role)}
        />
      </div>
      )}

      {!isResource && summary.repo_full_name && (
      <div className="mt-10">
        <DeployBadgeCard repoFullName={summary.repo_full_name} />
      </div>
      )}

      {!isResource && (
      <div className="mt-10">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("apps.domains.title")}</h2>
            <p className="text-sm text-gray-400 dark:text-gray-500">{t("apps.domains.subtitle")}</p>
          </div>
          {canMutate(role) && (
          <button
            onClick={() => { setDomainForm(defaultDomainForm(appName)); setIsDomainModalOpen(true); }}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-200 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            {t("apps.domains.add")}
          </button>
          )}
        </div>

        {isLoadingEndpoints ? (
          <div className="flex h-20 items-center justify-center"><Spinner /></div>
        ) : extraEndpoints.length === 0 && hostnames.length === 0 ? (
          <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 py-10">
            <svg className="mb-2 h-8 w-8 text-gray-300 dark:text-gray-700" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M21 12a9 9 0 01-9 9m9-9a9 9 0 00-9-9m9 9H3m9 9a9 9 0 01-9-9m9 9c1.657 0 3-4.03 3-9s-1.343-9-3-9m0 18c-1.657 0-3-4.03-3-9s1.343-9 3-9" />
            </svg>
            <p className="text-sm text-gray-400 dark:text-gray-500">{t("apps.domains.empty")}</p>
            <button
              onClick={() => { setDomainForm(defaultDomainForm(appName)); setIsDomainModalOpen(true); }}
              className="mt-2 text-sm text-blue-600 dark:text-blue-400 hover:text-blue-700"
            >
              {t("apps.domains.addFirst")}
            </button>
          </div>
        ) : (
          <div className="space-y-3">
            {hostnames.map((hn) => (
              <div key={hn.id} className="flex items-center justify-between rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-4 shadow-sm">
                <div className="flex items-center gap-4">
                  <Globe className="h-5 w-5 text-gray-400 dark:text-gray-500 shrink-0" />
                  <div>
                    <a
                      href={`https:${"/".repeat(2)}${hn.hostname}`}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="font-mono text-sm font-medium text-blue-600 dark:text-blue-400 hover:underline"
                    >
                      {hn.hostname}
                    </a>
                    <p className="text-xs text-gray-400 dark:text-gray-500">
                      {hn.managed ? t("apps.domains.managedDefault") : t("apps.domains.custom")}
                    </p>
                  </div>
                </div>
                <PhaseBadge phase={hn.status === "active" ? "Ready" : hn.status === "failed" ? "Failed" : "Pending"} />
              </div>
            ))}
            {extraEndpoints.map((ep) => {
              const epSummary = ep.summary_json as {
                fqdn?: string;
                auth_scheme?: string;
                swagger_enabled?: boolean;
                reason?: string;
                spec?: { dns?: { fqdn?: string } };
              };
              const fqdn = epSummary.spec?.dns?.fqdn ?? epSummary.fqdn;
              return (
                <div key={ep.id} className="flex items-center justify-between rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-4 shadow-sm">
                  <div className="flex items-center gap-4">
                    <svg className="h-5 w-5 text-gray-400 dark:text-gray-500 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M21 12a9 9 0 01-9 9m9-9a9 9 0 00-9-9m9 9H3m9 9a9 9 0 01-9-9m9 9c1.657 0 3-4.03 3-9s-1.343-9-3-9m0 18c-1.657 0-3-4.03-3-9s1.343-9 3-9" />
                    </svg>
                    <div>
                      {fqdn ? (
                        <a
                          href={`https:${"/".repeat(2)}${fqdn}`}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="font-mono text-sm font-medium text-blue-600 dark:text-blue-400 hover:underline"
                        >
                          {fqdn}
                        </a>
                      ) : (
                        <p className="font-mono text-sm font-medium text-gray-900 dark:text-gray-100">{ep.name}</p>
                      )}
                      <p className="text-xs text-gray-400 dark:text-gray-500">
                        {t("apps.domains.auth", { scheme: epSummary.auth_scheme ?? "none" })}
                        {epSummary.swagger_enabled && t("apps.domains.swagger")}
                      </p>
                      {ep.phase !== "Ready" && epSummary.reason && (
                        <p className="mt-0.5 text-xs text-amber-600 dark:text-amber-500">{epSummary.reason}</p>
                      )}
                    </div>
                  </div>
                  <PhaseBadge phase={ep.phase} />
                </div>
              );
            })}
          </div>
        )}
      </div>
      )}

      {!isResource && canMutate(role) && (
        <div className="mt-10 rounded-xl border border-red-200 dark:border-red-900 bg-white dark:bg-gray-900 px-5 py-5">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h2 className="text-sm font-semibold text-red-700 dark:text-red-400">{t("apps.dangerZone.title")}</h2>
              <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("apps.dangerZone.subtitle")}</p>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <button
                onClick={() => setIsMoveModalOpen(true)}
                className="inline-flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
              >
                {t("moveApp.button")}
              </button>
              <button
                onClick={() => setDeleteTarget({ kind: "app", projectId, envId, appName })}
                className="inline-flex items-center gap-2 rounded-lg border border-red-200 dark:border-red-900 bg-white dark:bg-gray-900 px-3 py-2 text-sm font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/30 transition-colors shadow-sm"
              >
                {t("apps.dangerZone.delete")}
              </button>
            </div>
          </div>
        </div>
      )}

      <Modal
        isOpen={isImageModalOpen}
        onClose={() => { setIsImageModalOpen(false); setImageSubmitError(null); }}
        title={t("apps.modal.image.title")}
      >
        <form onSubmit={handleImageUpdate} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("apps.modal.image.label")}</label>
            <input
              type="text"
              required
              value={newImage}
              onChange={(e) => setNewImage(e.target.value)}
              placeholder="ghcr.io/org/service:v2.0.0"
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("apps.modal.image.current")}<span className="font-mono">{summary.image ?? "—"}</span></p>
          </div>
          {imageSubmitError && (
            <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">{imageSubmitError}</div>
          )}
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => { setIsImageModalOpen(false); setImageSubmitError(null); }}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors">
              {t("common.cancel")}
            </button>
            <button type="submit" disabled={isImageSubmitting}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50 transition-colors">
              {isImageSubmitting ? <><Spinner size="sm" /> {t("apps.modal.image.submitting")}</> : t("apps.modal.image.submit")}
            </button>
          </div>
        </form>
      </Modal>

      <Modal
        isOpen={isDomainModalOpen}
        onClose={() => { setIsDomainModalOpen(false); setDomainSubmitError(null); }}
        title={t("apps.modal.domain.title")}
      >
        <form onSubmit={handleDomainCreate} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("apps.modal.domain.fqdn.label")}</label>
            <input
              type="text"
              required
              value={domainForm.fqdn}
              onChange={(e) => setDomainForm((f) => ({ ...f, fqdn: e.target.value }))}
              placeholder="api.myservice.ru"
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("apps.modal.domain.authScheme.label")}</label>
            <select
              value={domainForm.auth_scheme}
              onChange={(e) => setDomainForm((f) => ({ ...f, auth_scheme: e.target.value }))}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="none">{t("apps.modal.domain.authScheme.none")}</option>
              <option value="platform-jwt">platform-jwt</option>
              <option value="internal">internal</option>
            </select>
          </div>

          {domainForm.auth_scheme !== "none" && (
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
                {t("apps.modal.domain.scopes.label")} <span className="font-normal text-gray-400 dark:text-gray-500">{t("apps.modal.domain.scopes.hint")}</span>
              </label>
              <input
                type="text"
                value={domainForm.auth_scopes}
                onChange={(e) => setDomainForm((f) => ({ ...f, auth_scopes: e.target.value }))}
                placeholder="api.read, api.write"
                className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          )}

          <div className="flex items-center gap-3">
            <input
              type="checkbox"
              id="swagger-enabled"
              checked={domainForm.swagger_enabled}
              onChange={(e) => setDomainForm((f) => ({ ...f, swagger_enabled: e.target.checked }))}
              className="h-4 w-4 rounded border-gray-300 dark:border-gray-700 text-blue-600 dark:text-blue-400 focus:ring-blue-500"
            />
            <label htmlFor="swagger-enabled" className="text-sm font-medium text-gray-700 dark:text-gray-200">
              {t("apps.modal.domain.swagger.label")}
            </label>
          </div>

          {domainForm.swagger_enabled && (
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("apps.modal.domain.apiDocsPath.label")}</label>
                <input
                  type="text"
                  value={domainForm.swagger_path}
                  onChange={(e) => setDomainForm((f) => ({ ...f, swagger_path: e.target.value }))}
                  placeholder="/v3/api-docs"
                  className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("apps.modal.domain.apiTitle.label")}</label>
                <input
                  type="text"
                  value={domainForm.swagger_title}
                  onChange={(e) => setDomainForm((f) => ({ ...f, swagger_title: e.target.value }))}
                  placeholder={appName}
                  className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
            </div>
          )}

          {domainSubmitError && (
            <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">{domainSubmitError}</div>
          )}

          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => { setIsDomainModalOpen(false); setDomainSubmitError(null); }}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors">
              {t("common.cancel")}
            </button>
            <button type="submit" disabled={isDomainSubmitting}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50 transition-colors">
              {isDomainSubmitting ? <><Spinner size="sm" /> {t("apps.modal.domain.submitting")}</> : t("apps.modal.domain.submit")}
            </button>
          </div>
        </form>
      </Modal>

      {deleteTarget && (
        <DeleteImpactModal
          key={deleteImpactTargetKey(deleteTarget)}
          target={deleteTarget}
          onClose={() => setDeleteTarget(null)}
          onDeleted={handleAppDeleted}
        />
      )}

      {isMoveModalOpen && (
        <MoveAppModal
          projectId={projectId}
          envId={envId}
          appName={appName}
          onClose={() => setIsMoveModalOpen(false)}
          onMoved={handleAppMoved}
        />
      )}

    </div>
  );
}

"use client";
import { useEffect, useState, FormEvent } from "react";
import { useParams, useSearchParams, useRouter } from "next/navigation";
import Link from "next/link";
import { appsApi, endpointsApi } from "@/lib/api";
import type { ResourceSnapshot } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { Tooltip } from "@/components/ui/tooltip";
import { useProjectContext } from "@/lib/project-context";
import { canEditYaml, canMutate, canSeeTechnical } from "@/lib/rbac";
import { ComposeStatePanel } from "@/components/compose-state-panel";
import { MetricsPanel } from "@/components/metrics-panel";
import { LogsViewer } from "@/components/logs-viewer";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { useT } from "@/lib/i18n/console/context";

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

  const [isImageModalOpen, setIsImageModalOpen] = useState(false);
  const [newImage, setNewImage] = useState("");
  const [isImageSubmitting, setIsImageSubmitting] = useState(false);
  const [imageSubmitError, setImageSubmitError] = useState<string | null>(null);

  const [isDomainModalOpen, setIsDomainModalOpen] = useState(false);
  const [domainForm, setDomainForm] = useState<DomainForm>(defaultDomainForm(appName));
  const [isDomainSubmitting, setIsDomainSubmitting] = useState(false);
  const [domainSubmitError, setDomainSubmitError] = useState<string | null>(null);

  useEffect(() => {
    if (!envId) return;

    appsApi
      .list(projectId, envId)
      .then((data) => {
        const found = (data.apps ?? []).find((a) => a.name === appName);
        if (!found) setError(t("apps.detail.error.notFound"));
        else setApp(found);
      })
      .catch((err) => setError(err instanceof Error ? err.message : t("apps.detail.error.load")))
      .finally(() => setIsLoading(false));

    endpointsApi
      .list(projectId, envId, appName)
      .then((data) => setEndpoints(data.endpoints ?? []))
      .catch(() => setEndpoints([]))
      .finally(() => setIsLoadingEndpoints(false));
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
      <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
        {error ?? t("apps.detail.error.notFound")}
      </div>
    );
  }

  const summary = app.summary_json as { image?: string; port?: number; replicas?: number; profile?: string; runtime?: string };
  const isCompose = summary.runtime === "compose";

  return (
    <div>
      <div className="mb-8 flex items-start justify-between">
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
            <h1 className="text-2xl font-bold text-gray-900 font-mono">{appName}</h1>
            <PhaseBadge phase={app.phase} />
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Link
            href={`/projects/${projectId}/apps/${appName}/deployments${envId ? `?envId=${envId}` : ""}`}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm font-medium text-gray-700 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 16.5V9.75m0 0l3 3m-3-3l-3 3M6.75 19.5a4.5 4.5 0 01-1.41-8.775 5.25 5.25 0 0110.233-2.33 3 3 0 013.758 3.848A3.752 3.752 0 0118 19.5H6.75z" />
            </svg>
            {t("apps.detail.deployments")}
          </Link>
          <Link
            href={`/projects/${projectId}/apps/${appName}/settings${envId ? `?envId=${envId}` : ""}`}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm font-medium text-gray-700 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10.343 3.94c.09-.542.56-.94 1.11-.94h1.093c.55 0 1.02.398 1.11.94l.149.894c.07.424.384.764.78.93.398.164.855.142 1.205-.108l.737-.527a1.125 1.125 0 011.45.12l.773.774c.39.389.44 1.002.12 1.45l-.527.737c-.25.35-.272.806-.107 1.204.165.397.505.71.93.78l.893.15c.543.09.94.56.94 1.109v1.094c0 .55-.397 1.02-.94 1.11l-.893.149c-.425.07-.765.383-.93.78-.165.398-.143.854.107 1.204l.527.738c.32.447.269 1.06-.12 1.45l-.774.773a1.125 1.125 0 01-1.449.12l-.738-.527c-.35-.25-.806-.272-1.203-.107-.397.165-.71.505-.781.929l-.149.894c-.09.542-.56.94-1.11.94h-1.094c-.55 0-1.019-.398-1.11-.94l-.148-.894c-.071-.424-.384-.764-.781-.93-.398-.164-.854-.142-1.204.108l-.738.527c-.447.32-1.06.269-1.45-.12l-.773-.774a1.125 1.125 0 01-.12-1.45l.527-.737c.25-.35.273-.806.108-1.204-.165-.397-.505-.71-.93-.78l-.894-.15c-.542-.09-.94-.56-.94-1.109v-1.094c0-.55.398-1.02.94-1.11l.894-.149c.424-.07.765-.383.93-.78.165-.398.143-.854-.107-1.204l-.527-.738a1.125 1.125 0 01.12-1.45l.773-.773a1.125 1.125 0 011.45-.12l.737.527c.35.25.807.272 1.204.107.397-.165.71-.505.78-.929l.15-.894z" />
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
            </svg>
            {t("apps.detail.settings")}
          </Link>
          {canEditYaml(role) && (
          <Link
            href={`/projects/${projectId}/apps/${appName}/${isCompose ? "compose" : "values"}${envId ? `?envId=${envId}` : ""}`}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm font-medium text-gray-700 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
            </svg>
            {isCompose ? t("apps.detail.editCompose") : t("apps.detail.editValues")}
          </Link>
          )}
          {!isCompose && canMutate(role) && (
            <button
              onClick={() => { setNewImage(summary.image ?? ""); setIsImageModalOpen(true); }}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 transition-colors"
            >
              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12" />
              </svg>
              {t("apps.detail.deployImage")}
            </button>
          )}
        </div>
      </div>

      {isCompose ? (
        <div className="space-y-6">
          <ComposeStatePanel projectId={projectId} envId={envId} appName={appName} />
          <MetricsPanel kind="app" projectId={projectId} envId={envId} appName={appName} />
          <LogsViewer projectId={projectId} app={appName} />
        </div>
      ) : (
        <div className="space-y-6">
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            {[
              ...(canSeeTechnical(role) ? [{ label: t("apps.detail.spec.image"), value: summary.image ?? "—", mono: true, tip: summary.image }] : []),
              { label: t("apps.detail.spec.profile"), value: summary.profile ?? "small" },
              { label: t("apps.detail.spec.replicas"), value: String(summary.replicas ?? 2) },
              { label: t("apps.detail.spec.port"), value: String(summary.port ?? 8080) },
            ].map(({ label, value, mono, tip }: { label: string; value: string; mono?: boolean; tip?: string }) => (
              <div key={label} className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
                <p className="text-xs font-semibold uppercase tracking-wide text-gray-400">{label}</p>
                {tip && tip.length > 0 ? (
                  <Tooltip label={tip} className="mt-1 max-w-full">
                    <span className={`block truncate text-sm font-medium text-gray-900 ${mono ? "font-mono" : ""}`}>{value}</span>
                  </Tooltip>
                ) : (
                  <p className={`mt-1 text-sm font-medium text-gray-900 truncate ${mono ? "font-mono" : ""}`}>{value}</p>
                )}
              </div>
            ))}
          </div>
          <MetricsPanel kind="app" projectId={projectId} envId={envId} appName={appName} />
          <LogsViewer projectId={projectId} app={appName} />
        </div>
      )}

      <div className="mt-10">
        <div className="mb-4 flex items-center justify-between">
          <div>
            <h2 className="text-lg font-semibold text-gray-900">{t("apps.domains.title")}</h2>
            <p className="text-sm text-gray-400">{t("apps.domains.subtitle")}</p>
          </div>
          {canMutate(role) && (
          <button
            onClick={() => { setDomainForm(defaultDomainForm(appName)); setIsDomainModalOpen(true); }}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
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
        ) : endpoints.length === 0 ? (
          <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-gray-50 py-10">
            <svg className="mb-2 h-8 w-8 text-gray-300" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M21 12a9 9 0 01-9 9m9-9a9 9 0 00-9-9m9 9H3m9 9a9 9 0 01-9-9m9 9c1.657 0 3-4.03 3-9s-1.343-9-3-9m0 18c-1.657 0-3-4.03-3-9s1.343-9 3-9" />
            </svg>
            <p className="text-sm text-gray-400">{t("apps.domains.empty")}</p>
            <button
              onClick={() => { setDomainForm(defaultDomainForm(appName)); setIsDomainModalOpen(true); }}
              className="mt-2 text-sm text-blue-600 hover:text-blue-700"
            >
              {t("apps.domains.addFirst")}
            </button>
          </div>
        ) : (
          <div className="space-y-3">
            {endpoints.map((ep) => {
              const epSummary = ep.summary_json as { fqdn?: string; auth_scheme?: string; swagger_enabled?: boolean };
              return (
                <div key={ep.id} className="flex items-center justify-between rounded-xl border border-gray-200 bg-white px-5 py-4 shadow-sm">
                  <div className="flex items-center gap-4">
                    <svg className="h-5 w-5 text-gray-400 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M21 12a9 9 0 01-9 9m9-9a9 9 0 00-9-9m9 9H3m9 9a9 9 0 01-9-9m9 9c1.657 0 3-4.03 3-9s-1.343-9-3-9m0 18c-1.657 0-3-4.03-3-9s1.343-9 3-9" />
                    </svg>
                    <div>
                      <p className="font-mono text-sm font-medium text-gray-900">{epSummary.fqdn ?? ep.name}</p>
                      <p className="text-xs text-gray-400">
                        {t("apps.domains.auth", { scheme: epSummary.auth_scheme ?? "none" })}
                        {epSummary.swagger_enabled && t("apps.domains.swagger")}
                      </p>
                    </div>
                  </div>
                  <PhaseBadge phase={ep.phase} />
                </div>
              );
            })}
          </div>
        )}
      </div>

      <Modal
        isOpen={isImageModalOpen}
        onClose={() => { setIsImageModalOpen(false); setImageSubmitError(null); }}
        title={t("apps.modal.image.title")}
      >
        <form onSubmit={handleImageUpdate} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700">{t("apps.modal.image.label")}</label>
            <input
              type="text"
              required
              value={newImage}
              onChange={(e) => setNewImage(e.target.value)}
              placeholder="ghcr.io/org/service:v2.0.0"
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <p className="mt-1 text-xs text-gray-400">{t("apps.modal.image.current")}<span className="font-mono">{summary.image ?? "—"}</span></p>
          </div>
          {imageSubmitError && (
            <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{imageSubmitError}</div>
          )}
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => { setIsImageModalOpen(false); setImageSubmitError(null); }}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100 transition-colors">
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
            <label className="block text-sm font-medium text-gray-700">{t("apps.modal.domain.fqdn.label")}</label>
            <input
              type="text"
              required
              value={domainForm.fqdn}
              onChange={(e) => setDomainForm((f) => ({ ...f, fqdn: e.target.value }))}
              placeholder="api.myservice.ru"
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700">{t("apps.modal.domain.authScheme.label")}</label>
            <select
              value={domainForm.auth_scheme}
              onChange={(e) => setDomainForm((f) => ({ ...f, auth_scheme: e.target.value }))}
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="none">{t("apps.modal.domain.authScheme.none")}</option>
              <option value="platform-jwt">platform-jwt</option>
              <option value="api-key">api-key</option>
              <option value="internal">internal</option>
            </select>
          </div>

          {domainForm.auth_scheme !== "none" && (
            <div>
              <label className="block text-sm font-medium text-gray-700">
                {t("apps.modal.domain.scopes.label")} <span className="font-normal text-gray-400">{t("apps.modal.domain.scopes.hint")}</span>
              </label>
              <input
                type="text"
                value={domainForm.auth_scopes}
                onChange={(e) => setDomainForm((f) => ({ ...f, auth_scopes: e.target.value }))}
                placeholder="api.read, api.write"
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          )}

          <div className="flex items-center gap-3">
            <input
              type="checkbox"
              id="swagger-enabled"
              checked={domainForm.swagger_enabled}
              onChange={(e) => setDomainForm((f) => ({ ...f, swagger_enabled: e.target.checked }))}
              className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
            />
            <label htmlFor="swagger-enabled" className="text-sm font-medium text-gray-700">
              {t("apps.modal.domain.swagger.label")}
            </label>
          </div>

          {domainForm.swagger_enabled && (
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="block text-sm font-medium text-gray-700">{t("apps.modal.domain.apiDocsPath.label")}</label>
                <input
                  type="text"
                  value={domainForm.swagger_path}
                  onChange={(e) => setDomainForm((f) => ({ ...f, swagger_path: e.target.value }))}
                  placeholder="/v3/api-docs"
                  className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700">{t("apps.modal.domain.apiTitle.label")}</label>
                <input
                  type="text"
                  value={domainForm.swagger_title}
                  onChange={(e) => setDomainForm((f) => ({ ...f, swagger_title: e.target.value }))}
                  placeholder={appName}
                  className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
            </div>
          )}

          {domainSubmitError && (
            <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{domainSubmitError}</div>
          )}

          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => { setIsDomainModalOpen(false); setDomainSubmitError(null); }}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100 transition-colors">
              {t("common.cancel")}
            </button>
            <button type="submit" disabled={isDomainSubmitting}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50 transition-colors">
              {isDomainSubmitting ? <><Spinner size="sm" /> {t("apps.modal.domain.submitting")}</> : t("apps.modal.domain.submit")}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}

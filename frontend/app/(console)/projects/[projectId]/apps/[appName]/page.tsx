"use client";
import { useEffect, useState, FormEvent } from "react";
import { useParams, useSearchParams, useRouter } from "next/navigation";
import Link from "next/link";
import { appsApi, endpointsApi } from "@/lib/api";
import type { ResourceSnapshot } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";

function PhaseBadge({ phase }: { phase?: string }) {
  const p = phase ?? "";
  const isReady = p.toLowerCase() === "ready";
  return (
    <span className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
      isReady ? "bg-green-100 text-green-700" : "bg-yellow-100 text-yellow-700"
    }`}>
      {p || "Unknown"}
    </span>
  );
}

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
  const envId = searchParams.get("envId") ?? "";

  const [app, setApp] = useState<ResourceSnapshot | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [endpoints, setEndpoints] = useState<ResourceSnapshot[]>([]);
  const [isLoadingEndpoints, setIsLoadingEndpoints] = useState(true);

  // Image deploy modal
  const [isImageModalOpen, setIsImageModalOpen] = useState(false);
  const [newImage, setNewImage] = useState("");
  const [isImageSubmitting, setIsImageSubmitting] = useState(false);
  const [imageSubmitError, setImageSubmitError] = useState<string | null>(null);

  // Add domain modal
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
        if (!found) setError("App not found");
        else setApp(found);
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load app"))
      .finally(() => setIsLoading(false));

    endpointsApi
      .list(projectId, envId, appName)
      .then((data) => setEndpoints(data.endpoints ?? []))
      .catch(() => setEndpoints([]))
      .finally(() => setIsLoadingEndpoints(false));
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
      setTimeout(() => {
        router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
      }, 1500);
    } catch (err) {
      setImageSubmitError(err instanceof Error ? err.message : "Failed to update image");
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
      setTimeout(() => {
        router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
      }, 1500);
    } catch (err) {
      setDomainSubmitError(err instanceof Error ? err.message : "Failed to register domain");
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
        {error ?? "App not found"}
      </div>
    );
  }

  const summary = app.summary_json as { image?: string; port?: number; replicas?: number; profile?: string };

  return (
    <div>
      {/* Header */}
      <div className="mb-8 flex items-start justify-between">
        <div>
          <div className="flex items-center gap-2 text-sm text-gray-500">
            <Link href="/projects" className="hover:text-gray-700">Projects</Link>
            <span>/</span>
            <Link href={`/projects/${projectId}`} className="hover:text-gray-700">Overview</Link>
            <span>/</span>
            <Link href={`/projects/${projectId}/apps`} className="hover:text-gray-700">Applications</Link>
            <span>/</span>
            <span className="text-gray-900 font-mono">{appName}</span>
          </div>
          <div className="mt-2 flex items-center gap-3">
            <h1 className="text-2xl font-bold text-gray-900 font-mono">{appName}</h1>
            <PhaseBadge phase={app.phase} />
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Link
            href={`/projects/${projectId}/apps/${appName}/values${envId ? `?envId=${envId}` : ""}`}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm font-medium text-gray-700 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
            </svg>
            Edit values
          </Link>
          <button
            onClick={() => { setNewImage(summary.image ?? ""); setIsImageModalOpen(true); }}
            className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 transition-colors"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12" />
            </svg>
            Deploy Image
          </button>
        </div>
      </div>

      {/* Spec cards */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {[
          { label: "Image", value: summary.image ?? "—", mono: true },
          { label: "Profile", value: summary.profile ?? "small" },
          { label: "Replicas", value: String(summary.replicas ?? 2) },
          { label: "Port", value: String(summary.port ?? 8080) },
        ].map(({ label, value, mono }) => (
          <div key={label} className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
            <p className="text-xs font-semibold uppercase tracking-wide text-gray-400">{label}</p>
            <p className={`mt-1 text-sm font-medium text-gray-900 truncate ${mono ? "font-mono" : ""}`}>{value}</p>
          </div>
        ))}
      </div>

      {/* Domains section */}
      <div className="mt-10">
        <div className="mb-4 flex items-center justify-between">
          <div>
            <h2 className="text-lg font-semibold text-gray-900">Domains</h2>
            <p className="text-sm text-gray-400">Public endpoints via gateway + DNS</p>
          </div>
          <button
            onClick={() => { setDomainForm(defaultDomainForm(appName)); setIsDomainModalOpen(true); }}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            Add Domain
          </button>
        </div>

        {isLoadingEndpoints ? (
          <div className="flex h-20 items-center justify-center"><Spinner /></div>
        ) : endpoints.length === 0 ? (
          <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-gray-50 py-10">
            <svg className="mb-2 h-8 w-8 text-gray-300" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M21 12a9 9 0 01-9 9m9-9a9 9 0 00-9-9m9 9H3m9 9a9 9 0 01-9-9m9 9c1.657 0 3-4.03 3-9s-1.343-9-3-9m0 18c-1.657 0-3-4.03-3-9s1.343-9 3-9" />
            </svg>
            <p className="text-sm text-gray-400">No domains yet</p>
            <button
              onClick={() => { setDomainForm(defaultDomainForm(appName)); setIsDomainModalOpen(true); }}
              className="mt-2 text-sm text-blue-600 hover:text-blue-700"
            >
              Add first domain →
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
                        auth: {epSummary.auth_scheme ?? "none"}
                        {epSummary.swagger_enabled && " · swagger"}
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

      {/* Deploy Image Modal */}
      <Modal
        isOpen={isImageModalOpen}
        onClose={() => { setIsImageModalOpen(false); setImageSubmitError(null); }}
        title="Deploy New Image"
      >
        <form onSubmit={handleImageUpdate} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700">New Image Tag</label>
            <input
              type="text"
              required
              value={newImage}
              onChange={(e) => setNewImage(e.target.value)}
              placeholder="ghcr.io/org/service:v2.0.0"
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <p className="mt-1 text-xs text-gray-400">Current: <span className="font-mono">{summary.image ?? "—"}</span></p>
          </div>
          {imageSubmitError && (
            <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{imageSubmitError}</div>
          )}
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => { setIsImageModalOpen(false); setImageSubmitError(null); }}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100 transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={isImageSubmitting}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50 transition-colors">
              {isImageSubmitting ? <><Spinner size="sm" /> Deploying...</> : "Deploy"}
            </button>
          </div>
        </form>
      </Modal>

      {/* Add Domain Modal */}
      <Modal
        isOpen={isDomainModalOpen}
        onClose={() => { setIsDomainModalOpen(false); setDomainSubmitError(null); }}
        title="Add Domain"
      >
        <form onSubmit={handleDomainCreate} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700">FQDN</label>
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
            <label className="block text-sm font-medium text-gray-700">Auth Scheme</label>
            <select
              value={domainForm.auth_scheme}
              onChange={(e) => setDomainForm((f) => ({ ...f, auth_scheme: e.target.value }))}
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="none">none — public access</option>
              <option value="platform-jwt">platform-jwt</option>
              <option value="api-key">api-key</option>
              <option value="internal">internal</option>
            </select>
          </div>

          {domainForm.auth_scheme !== "none" && (
            <div>
              <label className="block text-sm font-medium text-gray-700">
                Scopes <span className="font-normal text-gray-400">(comma-separated)</span>
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
              Enable Swagger / OpenAPI
            </label>
          </div>

          {domainForm.swagger_enabled && (
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="block text-sm font-medium text-gray-700">API Docs Path</label>
                <input
                  type="text"
                  value={domainForm.swagger_path}
                  onChange={(e) => setDomainForm((f) => ({ ...f, swagger_path: e.target.value }))}
                  placeholder="/v3/api-docs"
                  className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700">API Title</label>
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
              Cancel
            </button>
            <button type="submit" disabled={isDomainSubmitting}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50 transition-colors">
              {isDomainSubmitting ? <><Spinner size="sm" /> Registering...</> : "Add Domain"}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}

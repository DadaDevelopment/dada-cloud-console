"use client";
import { FormEvent, useEffect, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import { appServersApi } from "@/lib/api";
import type { AppServer, AppServerState } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { CopyButton } from "@/components/ui/copy-button";
import { MetricsPanel } from "@/components/metrics-panel";
import { LogsViewer } from "@/components/logs-viewer";
import { useProjectContext } from "@/lib/project-context";
import { canMutate, canSeeTechnical } from "@/lib/rbac";
import { useT } from "@/lib/i18n/console/context";

export default function AppServerDetailPage() {
  const params = useParams<{ projectId: string; serverName: string }>();
  const { projectId, serverName } = params;
  const router = useRouter();
  const { t } = useT();
  const { role } = useProjectContext();
  const showTech = canSeeTechnical(role);
  const canManage = canMutate(role);

  const [server, setServer] = useState<AppServer | null>(null);
  const [state, setState] = useState<AppServerState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const [retryOpen, setRetryOpen] = useState(false);
  const [retrySubmitting, setRetrySubmitting] = useState(false);
  const [retryError, setRetryError] = useState<string | null>(null);
  const [retryForm, setRetryForm] = useState({ ssh_user: "root", ssh_port: "22", ssh_private_key: "" });

  async function handleRetry(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!server) return;
    setRetryError(null);
    setRetrySubmitting(true);
    try {
      const result = await appServersApi.create(projectId, {
        name: serverName,
        mode: "manual",
        vm_ip: (server.vm_ip ?? "").trim(),
        ssh_user: retryForm.ssh_user.trim() || "root",
        ssh_port: Number(retryForm.ssh_port) || 22,
        ssh_private_key: retryForm.ssh_private_key,
      });
      const opId = result.operation?.id;
      router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
    } catch (err) {
      setRetryError(err instanceof Error ? err.message : t("appServers.error.retry"));
    } finally {
      setRetrySubmitting(false);
    }
  }

  useEffect(() => {
    appServersApi
      .get(projectId, serverName)
      .then((d) => setServer(d.app_server))
      .catch((e) => setError(e instanceof Error ? e.message : t("appServers.error.loadDetail")))
      .finally(() => setLoading(false));
    appServersApi
      .getState(projectId, serverName)
      .then(setState)
      .catch(() => setState(null));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, serverName]);

  if (loading) {
    return <div className="flex h-64 items-center justify-center"><Spinner size="lg" /></div>;
  }
  if (error || !server) {
    return (
      <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
        {error ?? t("appServers.error.notFound")}
      </div>
    );
  }

  const cards = [
    { label: t("appServers.detail.status"), value: server.status },
    { label: t("appServers.detail.heartbeat"), value: state?.online ? t("appServers.detail.online") : t("appServers.detail.offline") },
    ...(showTech
      ? [
          { label: t("appServers.detail.vmIp"), value: server.vm_ip ?? "—", mono: true, copy: server.vm_ip ?? undefined },
          { label: t("appServers.detail.portainer"), value: server.portainer_endpoint_id != null ? String(server.portainer_endpoint_id) : "—", mono: true },
        ]
      : []),
  ];

  return (
    <div>
      <div className="mb-8">
        <Breadcrumb
          items={[
            { label: t("common.crumb.projects"), href: "/projects" },
            { label: t("common.crumb.overview"), href: `/projects/${projectId}` },
            { label: t("nav.app-servers"), href: `/projects/${projectId}/app-servers` },
            { label: serverName },
          ]}
        />
        <div className="mt-2 flex items-center gap-3">
          <h1 className="font-mono text-2xl font-bold text-gray-900">{serverName}</h1>
          <Badge className="bg-gray-100 text-gray-700">{server.source}</Badge>
          {server.status === "Ready" && (
            <span
              title={state?.online ? t("appServers.heartbeat.online") : t("appServers.heartbeat.none")}
              className={`inline-block h-2.5 w-2.5 rounded-full ${state?.online ? "bg-green-400" : "bg-gray-300"}`}
            />
          )}
          {canManage && server.status === "Failed" && server.source === "manual" && (
            <button
              onClick={() => {
                setRetryError(null);
                setRetryForm((prev) => ({ ...prev, ssh_private_key: "" }));
                setRetryOpen(true);
              }}
              className="ml-auto inline-flex items-center gap-2 rounded-lg bg-amber-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-amber-700"
            >
              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
              </svg>
              {t("appServers.retry.button")}
            </button>
          )}
        </div>
        {server.error_message && <p className="mt-1 text-sm text-red-600">{server.error_message}</p>}
      </div>

      <div className="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {cards.map(({ label, value, mono, copy }: { label: string; value: string; mono?: boolean; copy?: string }) => (
          <div key={label} className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
            <p className="text-xs font-semibold uppercase tracking-wide text-gray-400">{label}</p>
            <div className="mt-1 flex items-center justify-between gap-2">
              <p className={`text-sm font-medium text-gray-900 truncate ${mono ? "font-mono" : ""}`}>{value}</p>
              {copy && <CopyButton value={copy} className="shrink-0" />}
            </div>
          </div>
        ))}
      </div>

      <div className="mb-6">
        <MetricsPanel kind="vm" projectId={projectId} serverName={serverName} />
      </div>

      <LogsViewer projectId={projectId} vm={serverName} />

      <Modal isOpen={retryOpen} onClose={() => setRetryOpen(false)} title={t("appServers.retry.title")}>
        <form onSubmit={handleRetry} className="space-y-4">
          <p className="text-sm text-amber-700">{t("appServers.retry.help")}</p>

          <div className="grid gap-4 sm:grid-cols-[2fr_1fr]">
            <div>
              <label className="block text-sm font-medium text-gray-700">{t("appServers.field.vmIp.label")}</label>
              <input
                type="text"
                value={server.vm_ip ?? ""}
                readOnly
                className="mt-1 w-full rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 font-mono text-sm text-gray-600"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700">{t("appServers.field.sshPort.label")}</label>
              <input
                type="number"
                value={retryForm.ssh_port}
                onChange={(e) => setRetryForm((prev) => ({ ...prev, ssh_port: e.target.value }))}
                placeholder="22"
                className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
              />
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700">{t("appServers.field.sshUser.label")}</label>
            <input
              type="text"
              value={retryForm.ssh_user}
              onChange={(e) => setRetryForm((prev) => ({ ...prev, ssh_user: e.target.value }))}
              placeholder="root"
              className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700">{t("appServers.field.sshKey.label")}</label>
            <textarea
              required
              value={retryForm.ssh_private_key}
              onChange={(e) => setRetryForm((prev) => ({ ...prev, ssh_private_key: e.target.value }))}
              placeholder={"-----BEGIN OPENSSH PRIVATE KEY-----\n..."}
              rows={6}
              className="mt-1 w-full rounded-lg border border-gray-300 px-3 py-2 font-mono text-xs focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
            />
            <p className="mt-1 text-xs text-amber-700">{t("appServers.field.sshKey.warn")}</p>
          </div>

          {retryError && (
            <div role="alert" className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">
              {retryError}
            </div>
          )}

          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => setRetryOpen(false)}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100"
            >
              {t("common.cancel")}
            </button>
            <button
              type="submit"
              disabled={retrySubmitting}
              className="rounded-lg bg-amber-600 px-4 py-2 text-sm font-medium text-white hover:bg-amber-700 disabled:opacity-50"
            >
              {retrySubmitting ? t("common.creating") : t("appServers.retry.submit")}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}

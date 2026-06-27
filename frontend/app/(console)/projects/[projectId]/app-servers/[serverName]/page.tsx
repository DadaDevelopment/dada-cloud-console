"use client";
import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { appServersApi } from "@/lib/api";
import type { AppServer, AppServerState } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { CopyButton } from "@/components/ui/copy-button";
import { MetricsPanel } from "@/components/metrics-panel";
import { LogsViewer } from "@/components/logs-viewer";
import { useProjectContext } from "@/lib/project-context";
import { canSeeTechnical } from "@/lib/rbac";
import { useT } from "@/lib/i18n/console/context";

export default function AppServerDetailPage() {
  const params = useParams<{ projectId: string; serverName: string }>();
  const { projectId, serverName } = params;
  const { t } = useT();
  const { role } = useProjectContext();
  const showTech = canSeeTechnical(role);

  const [server, setServer] = useState<AppServer | null>(null);
  const [state, setState] = useState<AppServerState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

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
    </div>
  );
}

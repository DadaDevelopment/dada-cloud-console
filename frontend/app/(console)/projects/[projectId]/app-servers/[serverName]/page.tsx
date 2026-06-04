"use client";
import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { appServersApi } from "@/lib/api";
import type { AppServer, AppServerState } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import { MetricsPanel } from "@/components/metrics-panel";
import { LogsViewer } from "@/components/logs-viewer";

export default function AppServerDetailPage() {
  const params = useParams<{ projectId: string; serverName: string }>();
  const { projectId, serverName } = params;

  const [server, setServer] = useState<AppServer | null>(null);
  const [state, setState] = useState<AppServerState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    appServersApi
      .get(projectId, serverName)
      .then((d) => setServer(d.app_server))
      .catch((e) => setError(e instanceof Error ? e.message : "Failed to load app server"))
      .finally(() => setLoading(false));
    appServersApi
      .getState(projectId, serverName)
      .then(setState)
      .catch(() => setState(null));
  }, [projectId, serverName]);

  if (loading) {
    return <div className="flex h-64 items-center justify-center"><Spinner size="lg" /></div>;
  }
  if (error || !server) {
    return (
      <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
        {error ?? "App server not found"}
      </div>
    );
  }

  const cards = [
    { label: "Status", value: server.status },
    { label: "VM IP", value: server.vm_ip ?? "—", mono: true },
    { label: "Portainer", value: server.portainer_endpoint_id != null ? String(server.portainer_endpoint_id) : "—", mono: true },
    { label: "Heartbeat", value: state?.online ? "online" : "offline" },
  ];

  return (
    <div>
      {/* Header */}
      <div className="mb-8">
        <div className="flex items-center gap-2 text-sm text-gray-500">
          <Link href="/projects" className="hover:text-gray-700">Projects</Link>
          <span>/</span>
          <Link href={`/projects/${projectId}`} className="hover:text-gray-700">Overview</Link>
          <span>/</span>
          <Link href={`/projects/${projectId}/app-servers`} className="hover:text-gray-700">App Servers</Link>
          <span>/</span>
          <span className="font-mono text-gray-900">{serverName}</span>
        </div>
        <div className="mt-2 flex items-center gap-3">
          <h1 className="font-mono text-2xl font-bold text-gray-900">{serverName}</h1>
          <Badge className="bg-gray-100 text-gray-700">{server.source}</Badge>
          {server.status === "Ready" && (
            <span
              title={state?.online ? "Online (Portainer heartbeat)" : "No heartbeat"}
              className={`inline-block h-2.5 w-2.5 rounded-full ${state?.online ? "bg-green-400" : "bg-gray-300"}`}
            />
          )}
        </div>
        {server.error_message && <p className="mt-1 text-sm text-red-600">{server.error_message}</p>}
      </div>

      {/* Info cards */}
      <div className="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {cards.map(({ label, value, mono }) => (
          <div key={label} className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
            <p className="text-xs font-semibold uppercase tracking-wide text-gray-400">{label}</p>
            <p className={`mt-1 text-sm font-medium text-gray-900 truncate ${mono ? "font-mono" : ""}`}>{value}</p>
          </div>
        ))}
      </div>

      {/* Metrics */}
      <div className="mb-6">
        <MetricsPanel kind="vm" projectId={projectId} serverName={serverName} />
      </div>

      {/* Logs */}
      <LogsViewer projectId={projectId} vm={serverName} />
    </div>
  );
}

"use client";
import { useState } from "react";
import { appServersApi, projectsApi } from "@/lib/api";
import type { WorkloadDiscovery } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { CopyButton } from "@/components/ui/copy-button";
import { ImportWizard } from "./import-wizard";
import { useT } from "@/lib/i18n/console/context";

interface WorkloadsPanelProps {
  projectId: string;
  serverName: string;
  canDiscover: boolean;
  canManage: boolean;
}

/**
 * Workloads tab of an AppServer. Reframes read-only discovery as the on-ramp to
 * adoption: discover → review running containers → Import into a managed compose
 * app. Read-only until the user explicitly hits Import (review-before-change).
 */
export function WorkloadsPanel({ projectId, serverName, canDiscover, canManage }: WorkloadsPanelProps) {
  const { t } = useT();
  const [discovering, setDiscovering] = useState(false);
  const [discovery, setDiscovery] = useState<WorkloadDiscovery | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [wizardOpen, setWizardOpen] = useState(false);

  async function handleDiscover() {
    setError(null);
    setDiscovery(null);
    setDiscovering(true);
    try {
      const { operation } = await appServersApi.discover(projectId, serverName);
      let op = operation;
      for (let i = 0; i < 40 && op.status !== "Ready" && op.status !== "Failed"; i++) {
        await new Promise((r) => setTimeout(r, 1500));
        op = (await projectsApi.getOperation(projectId, op.id)).operation;
      }
      if (op.status === "Failed") throw new Error(op.error_message || t("appServers.discover.failed"));
      if (op.status !== "Ready") throw new Error(t("appServers.discover.timeout"));
      setDiscovery((op.validation_result as WorkloadDiscovery) ?? null);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("appServers.discover.failed"));
    } finally {
      setDiscovering(false);
    }
  }

  if (!canDiscover) {
    return (
      <div className="rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 px-6 py-12 text-center">
        <p className="text-sm font-medium text-gray-600 dark:text-gray-300">{t("appServers.workloads.notEnrolled.title")}</p>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("appServers.workloads.notEnrolled.desc")}</p>
      </div>
    );
  }

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-base font-semibold text-gray-900 dark:text-gray-100">{t("appServers.workloads.title")}</h2>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("appServers.workloads.subtitle")}</p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={handleDiscover}
            disabled={discovering}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-300 dark:border-gray-700 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 transition-colors hover:bg-gray-100 dark:hover:bg-gray-800 disabled:opacity-50"
          >
            {discovering ? <Spinner size="sm" /> : (
              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M21 21l-4.35-4.35M11 19a8 8 0 100-16 8 8 0 000 16z" /></svg>
            )}
            {discovery ? t("appServers.workloads.rescan") : t("appServers.discover.button")}
          </button>
          {discovery && discovery.containers.length > 0 && canManage && (
            <button
              onClick={() => setWizardOpen(true)}
              className="inline-flex items-center gap-2 rounded-lg bg-amber-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-amber-700"
            >
              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" /></svg>
              {t("appServers.workloads.importCta")}
            </button>
          )}
        </div>
      </div>

      {error && (
        <div role="alert" className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">{error}</div>
      )}

      {!discovery && !discovering && !error && (
        <div className="rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 px-6 py-14 text-center">
          <svg className="mx-auto mb-3 h-10 w-10 text-gray-300 dark:text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5" /></svg>
          <p className="text-sm font-semibold text-gray-700 dark:text-gray-200">{t("appServers.workloads.empty.title")}</p>
          <p className="mx-auto mt-1 max-w-sm text-sm text-gray-500 dark:text-gray-400">{t("appServers.workloads.empty.desc")}</p>
        </div>
      )}

      {discovering && (
        <div className="flex items-center justify-center rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 py-14">
          <div className="flex items-center gap-3 text-sm text-gray-500 dark:text-gray-400"><Spinner size="md" /> {t("appServers.workloads.scanning")}</div>
        </div>
      )}

      {discovery && (
        <div className="space-y-4">
          <div className="flex items-center gap-2 text-xs text-gray-400 dark:text-gray-500">
            <span className="inline-flex items-center gap-1 rounded-full bg-gray-100 dark:bg-gray-800 px-2 py-0.5 font-medium text-gray-600 dark:text-gray-300">
              {t("appServers.workloads.found", { n: discovery.containers.length })}
            </span>
            <span>{t("appServers.discover.readonly")}</span>
          </div>

          {discovery.containers.length === 0 ? (
            <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-6 py-10 text-center text-sm text-gray-500 dark:text-gray-400">
              {t("appServers.workloads.none")}
            </div>
          ) : (
            <div className="grid gap-3 sm:grid-cols-2">
              {discovery.containers.map((c) => {
                const vols = c.mounts.filter((m) => m.type === "volume" && m.name);
                const running = c.state === "running";
                return (
                  <div key={c.name} className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4 shadow-sm">
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{c.name}</span>
                      <span className={`inline-flex shrink-0 items-center gap-1 text-[11px] font-medium ${running ? "text-green-600 dark:text-green-400" : "text-gray-400 dark:text-gray-500"}`}>
                        <span className={`h-1.5 w-1.5 rounded-full ${running ? "bg-green-500" : "bg-gray-300 dark:bg-gray-600"}`} />
                        {c.state}
                      </span>
                    </div>
                    <p className="mt-1 truncate font-mono text-xs text-gray-500 dark:text-gray-400">{c.image}</p>
                    <div className="mt-3 flex flex-wrap items-center gap-1.5">
                      {c.ports.length === 0 && vols.length === 0 && (
                        <span className="text-[11px] text-gray-400 dark:text-gray-500">{t("appServers.workloads.noPortsVols")}</span>
                      )}
                      {c.ports.map((p) => (
                        <span key={p} className="rounded bg-blue-50 dark:bg-blue-950/40 px-1.5 py-0.5 font-mono text-[11px] text-blue-700 dark:text-blue-300">{p}</span>
                      ))}
                      {vols.map((m) => (
                        <span key={m.destination} className="inline-flex items-center gap-1 rounded bg-green-50 dark:bg-green-950/40 px-1.5 py-0.5 font-mono text-[11px] text-green-700 dark:text-green-300" title={t("appServers.import.services.dataSafe")}>
                          <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" /></svg>
                          {m.name}
                        </span>
                      ))}
                    </div>
                  </div>
                );
              })}
            </div>
          )}

          {discovery.warnings.length > 0 && (
            <div className="rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/30 p-3">
              <p className="mb-1 text-xs font-semibold text-amber-800 dark:text-amber-200">{t("appServers.workloads.warnings")}</p>
              <ul className="space-y-0.5 text-xs text-amber-700 dark:text-amber-300">
                {discovery.warnings.map((w, i) => <li key={i}>· {w}</li>)}
              </ul>
            </div>
          )}

          {discovery.external_volumes_yaml.trim() && (
            <details className="rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900">
              <summary className="flex cursor-pointer items-center justify-between px-4 py-3 text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
                {t("appServers.discover.externalVolumes")}
                <CopyButton value={discovery.external_volumes_yaml} />
              </summary>
              <pre className="overflow-x-auto border-t border-gray-100 dark:border-gray-800 p-3 font-mono text-xs text-gray-800 dark:text-gray-200">{discovery.external_volumes_yaml}</pre>
            </details>
          )}
        </div>
      )}

      {discovery && wizardOpen && (
        <ImportWizard projectId={projectId} serverName={serverName} discovery={discovery} isOpen={wizardOpen} onClose={() => setWizardOpen(false)} />
      )}
    </div>
  );
}

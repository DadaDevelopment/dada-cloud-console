"use client";
import { useEffect, useState } from "react";
import { AlertTriangle, Globe, Database, HardDrive, Network, ShieldCheck, Boxes } from "lucide-react";
import { appsApi, projectsApi } from "@/lib/api";
import type { DeleteImpactGroup, DeleteImpactItem, DeleteImpactResponse, OperationResponse } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";

export type DeleteImpactTarget =
  | { kind: "app"; projectId: string; envId: string; appName: string }
  | { kind: "project"; projectId: string; projectName: string };

interface DeleteImpactModalProps {
  target: DeleteImpactTarget;
  onClose: () => void;
  onDeleted: (result: OperationResponse) => void;
}

/** Stable per-target key so the parent can force a fresh mount (and fresh
 * local state) each time a different app/project is targeted for delete. */
export function deleteImpactTargetKey(target: DeleteImpactTarget): string {
  return target.kind === "app" ? `app:${target.projectId}:${target.envId}:${target.appName}` : `project:${target.projectId}`;
}

const groupOrder: DeleteImpactGroup[] = ["domain", "database", "storage", "ingress", "certificate", "other"];

const groupIcons: Record<DeleteImpactGroup, typeof Globe> = {
  domain: Globe,
  database: Database,
  storage: HardDrive,
  ingress: Network,
  certificate: ShieldCheck,
  other: Boxes,
};

function groupItems(items: DeleteImpactItem[]): Map<DeleteImpactGroup, DeleteImpactItem[]> {
  const map = new Map<DeleteImpactGroup, DeleteImpactItem[]>();
  for (const group of groupOrder) map.set(group, []);
  for (const item of items) {
    const bucket = map.get(item.group) ?? map.get("other")!;
    bucket.push(item);
  }
  return map;
}

export function DeleteImpactModal({ target, onClose, onDeleted }: DeleteImpactModalProps) {
  const { t } = useT();
  const [impact, setImpact] = useState<DeleteImpactResponse | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [confirmName, setConfirmName] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  const expectedName = target.kind === "app" ? target.appName : target.projectName;

  useEffect(() => {
    let cancelled = false;
    const fetcher =
      target.kind === "app"
        ? appsApi.getDeleteImpact(target.projectId, target.envId, target.appName)
        : projectsApi.getDeleteImpact(target.projectId);
    fetcher
      .then((data) => !cancelled && setImpact(data))
      .catch((err) => !cancelled && setLoadError(err instanceof Error ? err.message : t("deleteImpact.error.load")))
      .finally(() => !cancelled && setIsLoading(false));
    return () => {
      cancelled = true;
    };
  }, [target, t]);

  async function handleSubmit() {
    if (confirmName !== expectedName) return;
    setIsSubmitting(true);
    setSubmitError(null);
    try {
      const result =
        target.kind === "app"
          ? await appsApi.remove(target.projectId, target.envId, target.appName)
          : await projectsApi.remove(target.projectId);
      onDeleted(result);
    } catch (err) {
      const key = target.kind === "app" ? "deleteImpact.error.submit.app" : "deleteImpact.error.submit.project";
      setSubmitError(err instanceof Error ? err.message : t(key));
    } finally {
      setIsSubmitting(false);
    }
  }

  const clusterOnlyItems = (impact?.items ?? []).filter((item) => item.source === "cluster-only");
  const grouped = impact ? groupItems(impact.items) : null;
  const confirmDisabled = confirmName !== expectedName || isSubmitting || isLoading;

  return (
    <Modal isOpen onClose={onClose} title={t(target.kind === "app" ? "deleteImpact.title.app" : "deleteImpact.title.project")}>
      <div className="space-y-4">
        {isLoading ? (
          <div className="flex flex-col items-center gap-3 py-8">
            <Spinner size="lg" />
            <p className="text-sm text-gray-500 dark:text-gray-400">{t("deleteImpact.loading")}</p>
          </div>
        ) : loadError ? (
          <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
            {loadError}
          </div>
        ) : (
          <>
            {impact && !impact.cluster_scan && (
              <div className="flex gap-2 rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/30 px-4 py-3 text-sm text-amber-700 dark:text-amber-300">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
                <span>{t("deleteImpact.banner.noScan")}</span>
              </div>
            )}

            {clusterOnlyItems.length > 0 && (
              <div className="rounded-lg border border-red-300 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
                <div className="flex gap-2">
                  <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
                  <span className="font-medium">{t("deleteImpact.banner.clusterOnly")}</span>
                </div>
                <ul className="mt-2 space-y-1 pl-6">
                  {clusterOnlyItems.map((item) => (
                    <li key={`${item.kind}-${item.name}`} className="font-mono text-xs">
                      {item.kind}/{item.name}
                    </li>
                  ))}
                </ul>
              </div>
            )}

            {impact && impact.items.length === 0 ? (
              <p className="text-sm text-gray-500 dark:text-gray-400">{t("deleteImpact.empty")}</p>
            ) : (
              grouped && (
                <div className="space-y-2">
                  {groupOrder
                    .filter((group) => (grouped.get(group) ?? []).length > 0)
                    .map((group) => {
                      const groupedItems = grouped.get(group) ?? [];
                      const Icon = groupIcons[group];
                      return (
                        <div
                          key={group}
                          className="rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-4 py-3"
                        >
                          <div className="flex items-center gap-2 text-sm font-medium text-gray-900 dark:text-gray-100">
                            <Icon className="h-4 w-4 text-gray-400 dark:text-gray-500" />
                            {t(`deleteImpact.group.${group}`)}
                            <span className="text-xs font-normal text-gray-400 dark:text-gray-500">{groupedItems.length}</span>
                          </div>
                          <ul className="mt-1.5 space-y-1 pl-6">
                            {groupedItems.map((item) => (
                              <li
                                key={`${item.kind}-${item.name}`}
                                className="flex items-center justify-between gap-2 font-mono text-xs text-gray-600 dark:text-gray-400"
                              >
                                <span>{item.name}</span>
                                <span
                                  className={
                                    item.source === "cluster-only"
                                      ? "shrink-0 rounded-full bg-red-50 dark:bg-red-950/40 px-2 py-0.5 font-sans text-[11px] text-red-600 dark:text-red-400"
                                      : "shrink-0 rounded-full bg-gray-100 dark:bg-gray-800 px-2 py-0.5 font-sans text-[11px] text-gray-500 dark:text-gray-400"
                                  }
                                >
                                  {t(item.source === "cluster-only" ? "deleteImpact.source.clusterOnly" : "deleteImpact.source.console")}
                                </span>
                              </li>
                            ))}
                          </ul>
                        </div>
                      );
                    })}
                </div>
              )
            )}

            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
                {t(target.kind === "app" ? "deleteImpact.confirm.label.app" : "deleteImpact.confirm.label.project", { name: expectedName })}
              </label>
              <input
                type="text"
                value={confirmName}
                onChange={(e) => setConfirmName(e.target.value)}
                placeholder={expectedName}
                className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-red-500 focus:outline-none focus:ring-1 focus:ring-red-500"
              />
            </div>

            {submitError && (
              <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
                {submitError}
              </div>
            )}

            <div className="flex justify-end gap-3 pt-2">
              <button
                type="button"
                onClick={onClose}
                className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
              >
                {t("common.cancel")}
              </button>
              <button
                type="button"
                onClick={handleSubmit}
                disabled={confirmDisabled}
                className="inline-flex items-center gap-2 rounded-lg bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
              >
                {isSubmitting ? (
                  <>
                    <Spinner size="sm" /> {t("deleteImpact.submitting")}
                  </>
                ) : (
                  t(target.kind === "app" ? "deleteImpact.submit.app" : "deleteImpact.submit.project")
                )}
              </button>
            </div>
          </>
        )}
      </div>
    </Modal>
  );
}

"use client";
import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, ArrowRight } from "lucide-react";
import { appsApi, projectsApi } from "@/lib/api";
import type { MoveImpactResponse, OperationResponse, Project } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";

interface MoveAppModalProps {
  projectId: string;
  envId: string;
  appName: string;
  onClose: () => void;
  onMoved: (result: OperationResponse) => void;
}

export function MoveAppModal({ projectId, envId, appName, onClose, onMoved }: MoveAppModalProps) {
  const { t } = useT();
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [projectsError, setProjectsError] = useState<string | null>(null);
  const [targetProjectId, setTargetProjectId] = useState("");

  const [impact, setImpact] = useState<MoveImpactResponse | null>(null);
  const [isLoadingImpact, setIsLoadingImpact] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    projectsApi
      .list()
      .then((data) => !cancelled && setProjects((data.projects ?? []).filter((p) => p.id !== projectId)))
      .catch((err) => !cancelled && setProjectsError(err instanceof Error ? err.message : t("moveApp.target.errorLoad")));
    return () => {
      cancelled = true;
    };
  }, [projectId, t]);

  useEffect(() => {
    if (!targetProjectId) return;
    let cancelled = false;
    appsApi
      .getMoveImpact(projectId, envId, appName, targetProjectId)
      .then((data) => !cancelled && setImpact(data))
      .catch((err) => !cancelled && setLoadError(err instanceof Error ? err.message : t("moveApp.error.load")))
      .finally(() => !cancelled && setIsLoadingImpact(false));
    return () => {
      cancelled = true;
    };
  }, [projectId, envId, appName, targetProjectId, t]);

  function handleTargetChange(nextTargetProjectId: string) {
    setTargetProjectId(nextTargetProjectId);
    setImpact(null);
    setLoadError(null);
    setSubmitError(null);
    setIsLoadingImpact(!!nextTargetProjectId);
  }

  async function handleSubmit() {
    if (!targetProjectId || !impact?.can_move) return;
    setIsSubmitting(true);
    setSubmitError(null);
    try {
      const result = await appsApi.move(projectId, envId, appName, targetProjectId);
      onMoved(result);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : t("moveApp.error.submit"));
    } finally {
      setIsSubmitting(false);
    }
  }

  const blockers = impact?.blockers ?? [];
  const isBlocked = blockers.length > 0 || !!impact?.name_collision;
  const confirmDisabled = !targetProjectId || !impact || isBlocked || isSubmitting || isLoadingImpact;

  const targetLabel = useMemo(() => {
    const target = projects?.find((p) => p.id === targetProjectId);
    return target ? target.display_name || target.name : "";
  }, [projects, targetProjectId]);

  return (
    <Modal isOpen onClose={onClose} title={t("moveApp.title")}>
      <div className="space-y-4">
        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("moveApp.target.label")}</label>
          {projectsError ? (
            <div className="mt-1 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
              {projectsError}
            </div>
          ) : projects === null ? (
            <div className="mt-2 flex items-center gap-2 text-sm text-gray-500 dark:text-gray-400">
              <Spinner size="sm" />
            </div>
          ) : projects.length === 0 ? (
            <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("moveApp.target.empty")}</p>
          ) : (
            <select
              value={targetProjectId}
              onChange={(e) => handleTargetChange(e.target.value)}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="">{t("moveApp.target.placeholder")}</option>
              {projects.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.display_name || p.name}
                </option>
              ))}
            </select>
          )}
        </div>

        {targetProjectId && (
          <>
            {isLoadingImpact ? (
              <div className="flex flex-col items-center gap-3 py-8">
                <Spinner size="lg" />
                <p className="text-sm text-gray-500 dark:text-gray-400">{t("moveApp.loading")}</p>
              </div>
            ) : loadError ? (
              <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
                {loadError}
              </div>
            ) : impact ? (
              <>
                <div className="flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900/60 px-4 py-3 text-sm font-mono text-gray-600 dark:text-gray-400">
                  <span className="truncate">{impact.src_project}</span>
                  <ArrowRight className="h-4 w-4 shrink-0 text-gray-400 dark:text-gray-500" />
                  <span className="truncate">{targetLabel || impact.target_project}</span>
                </div>

                <p className="text-xs text-gray-400 dark:text-gray-500">
                  {t("moveApp.summary.namespace", { namespace: impact.target_namespace })}
                </p>

                {isBlocked && (
                  <div className="rounded-lg border border-red-300 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
                    <div className="flex gap-2">
                      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
                      <span className="font-medium">{t("moveApp.banner.blocked.title")}</span>
                    </div>
                    <ul className="mt-2 space-y-1 pl-6">
                      {impact.name_collision && <li>{t("moveApp.banner.nameCollision")}</li>}
                      {blockers.map((b) => (
                        <li key={`${b.kind}-${b.name}`}>
                          <span className="font-mono text-xs">{b.kind}/{b.name}</span> — {b.reason}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}

                <div>
                  <p className="text-sm font-medium text-gray-900 dark:text-gray-100">{t("moveApp.summary.title")}</p>
                  {impact.movable.length === 0 ? (
                    <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("moveApp.summary.empty")}</p>
                  ) : (
                    <ul className="mt-1.5 space-y-1 pl-1">
                      {impact.movable.map((item) => (
                        <li
                          key={`${item.kind}-${item.name}`}
                          className="flex items-center justify-between gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-2 font-mono text-xs text-gray-600 dark:text-gray-400"
                        >
                          <span>{item.name}</span>
                          <span className="shrink-0 rounded-full bg-gray-100 dark:bg-gray-800 px-2 py-0.5 font-sans text-[11px] text-gray-500 dark:text-gray-400">
                            {item.kind}
                          </span>
                        </li>
                      ))}
                    </ul>
                  )}
                </div>

                {!isBlocked && (
                  <p className="text-xs text-gray-400 dark:text-gray-500">{t("moveApp.downtime.hint")}</p>
                )}
              </>
            ) : null}
          </>
        )}

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
            className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
          >
            {isSubmitting ? (
              <>
                <Spinner size="sm" /> {t("moveApp.submitting")}
              </>
            ) : (
              t("moveApp.submit")
            )}
          </button>
        </div>
      </div>
    </Modal>
  );
}

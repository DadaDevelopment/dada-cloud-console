"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { cloudTasksApi } from "@/lib/api";
import { useT } from "@/lib/i18n/console/context";
import type { CloudTask } from "@/lib/types";

const CATALOG: { taskType: string; labelKey: string; appliesTo: (k: string) => boolean }[] = [
  { taskType: "yandex-metrika-goals", labelKey: "cloudTasks.label.metrika", appliesTo: (k) => k === "web" || k === "App" },
];

const TASK_LABEL_KEYS: Record<string, string> = {
  "yandex-metrika-goals": "cloudTasks.label.metrika",
  autofix: "cloudTasks.label.autofix",
};

/**
 * CloudTaskPanel renders the agent-task chips for an app plus a live status card
 * per fired task. Running tasks are polled every 3s until they settle.
 *
 * `highlightTaskType` lets a caller that just fired a task (e.g. the
 * "Auto-fix with AI" button elsewhere on the app page, which redirects here
 * with `#agent`) mark the newest matching row so the user actually notices
 * it landed instead of scanning an unlabeled "Агент" section for a chip they
 * cannot tell apart from an old one. This section previously had zero
 * indication of *which* task was the one just fired, and the client-side
 * router.push hash navigation does not reliably scroll an anchor into view
 * on its own -- so without this, a user who triggered an auto-fix saw no
 * visible change on the page at all and had to go hunting for it (reported
 * live: "юзер даже не узнает"). The highlight is scoped to the single
 * newest row of that type (tasks are already newest-first) and to the
 * duration of this page view -- it does not persist across a reload, since
 * `highlightTaskType` comes from a one-shot query param the caller sets, not
 * from stored state.
 */
export function CloudTaskPanel(props: {
  projectId: string;
  envId: string;
  appName: string;
  appKind: string;
  canMutate: boolean;
  highlightTaskType?: string;
}) {
  const { projectId, envId, appName, appKind, canMutate, highlightTaskType } = props;
  const { t } = useT();
  const [tasks, setTasks] = useState<CloudTask[]>([]);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const sectionRef = useRef<HTMLDivElement | null>(null);
  const hasScrolledRef = useRef(false);

  const load = useCallback(() => {
    void cloudTasksApi
      .list(projectId, envId, appName)
      .then((d) => setTasks(d.cloud_tasks ?? []))
      .catch((e) => setError(e instanceof Error ? e.message : t("cloudTasks.loadFailed")));
  }, [projectId, envId, appName, t]);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    if (!tasks.some((task) => task.status === "running")) return;
    const id = setInterval(load, 3000);
    return () => clearInterval(id);
  }, [tasks, load]);

  /**
   * Scroll this section into view once, the first time its data has
   * actually loaded, when the caller flagged a task type it just fired.
   * Gated on `tasks` having loaded (not on mount) because a hash-anchor
   * scroll racing the initial fetch is exactly the failure mode this
   * exists to fix: jumping to an empty section before its content painted
   * looks identical to not jumping at all.
   */
  useEffect(() => {
    if (!highlightTaskType || hasScrolledRef.current) return;
    if (tasks.length === 0) return;
    hasScrolledRef.current = true;
    sectionRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
  }, [highlightTaskType, tasks]);

  const run = async (taskType: string) => {
    setBusy(taskType);
    setError(null);
    try {
      const { cloud_task } = await cloudTasksApi.create(projectId, envId, appName, taskType);
      setTasks((prev) => [cloud_task, ...prev]);
    } catch (e) {
      setError(e instanceof Error ? e.message : t("cloudTasks.runFailed"));
    } finally {
      setBusy(null);
    }
  };

  const chips = CATALOG.filter((e) => e.appliesTo(appKind));
  if (chips.length === 0 && tasks.length === 0) return null;

  // The newest row of the just-fired type -- tasks are already newest-first
  // (backend orders by created_at DESC), so the first match is the one the
  // caller means, never a stale earlier run of the same task type.
  const highlightedTaskID = highlightTaskType
    ? tasks.find((task) => task.task_type === highlightTaskType)?.id
    : undefined;

  return (
    <section ref={sectionRef} className="space-y-4">
      <h3 className="text-sm font-semibold text-gray-700">{t("cloudTasks.title")}</h3>
      {error && <p className="text-sm text-red-600">{error}</p>}
      <div className="flex flex-wrap gap-2">
        {chips.map((e) => (
          <button
            key={e.taskType}
            disabled={!canMutate || busy !== null}
            onClick={() => run(e.taskType)}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm font-medium text-gray-700 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm disabled:opacity-50"
          >
            {busy === e.taskType ? t("cloudTasks.starting") : t("cloudTasks.run", { label: t(e.labelKey) })}
          </button>
        ))}
      </div>
      <ul className="space-y-2">
        {tasks.map((task) => (
          <li
            key={task.id}
            className={
              task.id === highlightedTaskID
                ? "rounded-xl border-2 border-blue-400 dark:border-blue-500 bg-blue-50/60 dark:bg-blue-950/30 p-4 shadow-sm ring-2 ring-blue-200 dark:ring-blue-900"
                : "rounded-xl border border-gray-200 bg-white p-4 shadow-sm"
            }
          >
            {task.id === highlightedTaskID && (
              <p className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-blue-600 dark:text-blue-400">
                {t("cloudTasks.justFired")}
              </p>
            )}
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium text-gray-900">
                {TASK_LABEL_KEYS[task.task_type] ? t(TASK_LABEL_KEYS[task.task_type]) : task.task_type}
              </span>
              <StatusBadge status={task.status} label={t(`cloudTasks.status.${task.status}`)} />
            </div>
            {task.error && <p className="mt-1 text-xs text-red-600">{task.error}</p>}
            {task.pr_url && (
              <a
                href={task.pr_url}
                target="_blank"
                rel="noreferrer"
                className="mt-2 inline-block text-sm font-medium text-blue-600 hover:underline"
              >
                {t("cloudTasks.viewPR")}
              </a>
            )}
            {task.artifacts.length > 0 && (
              <ul className="mt-2 space-y-1">
                {task.artifacts.map((a) => (
                  <li key={a.file_id}>
                    <a
                      className="text-sm text-gray-700 hover:text-blue-600"
                      href={cloudTasksApi.artifactUrl(projectId, task.id, a.file_id)}
                      target="_blank"
                      rel="noreferrer"
                    >
                      {a.name} ({Math.round(a.size / 1024)} KB)
                    </a>
                  </li>
                ))}
              </ul>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

function StatusBadge({ status, label }: { status: CloudTask["status"]; label: string }) {
  const color =
    status === "completed"
      ? "bg-green-100 text-green-700"
      : status === "failed"
        ? "bg-red-100 text-red-700"
        : status === "canceled"
          ? "bg-gray-100 text-gray-600"
          : "bg-blue-100 text-blue-700";
  return <span className={`rounded-full px-2 py-0.5 text-xs font-semibold ${color}`}>{label}</span>;
}

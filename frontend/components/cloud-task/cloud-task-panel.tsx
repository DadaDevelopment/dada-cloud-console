"use client";

import { useCallback, useEffect, useState } from "react";
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
 */
export function CloudTaskPanel(props: {
  projectId: string;
  envId: string;
  appName: string;
  appKind: string;
  canMutate: boolean;
}) {
  const { projectId, envId, appName, appKind, canMutate } = props;
  const { t } = useT();
  const [tasks, setTasks] = useState<CloudTask[]>([]);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

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

  return (
    <section className="space-y-4">
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
          <li key={task.id} className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm">
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

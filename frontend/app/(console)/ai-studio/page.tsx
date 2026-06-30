"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import { projectsApi, mlflowApi } from "@/lib/api";
import type { Project, MLflowRegisteredModel } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { DataTable, type Column } from "@/components/ui/data-table";
import { useT } from "@/lib/i18n/console/context";

function fmtTimestamp(ms?: number): string {
  if (!ms) return "—";
  return new Date(ms).toLocaleString();
}

export default function AIStudioRegistryPage() {
  const { t } = useT();
  const [projects, setProjects] = useState<Project[]>([]);
  const [selectedProjectId, setSelectedProjectId] = useState<string>("");
  const [models, setModels] = useState<MLflowRegisteredModel[]>([]);
  const [warning, setWarning] = useState<string | null>(null);
  const [isLoadingProjects, setIsLoadingProjects] = useState(true);
  const [isLoadingModels, setIsLoadingModels] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    projectsApi
      .list()
      .then((data) => {
        const list = data.projects ?? [];
        setProjects(list);
        if (list.length > 0) setSelectedProjectId(list[0].id);
      })
      .catch((err) => setError(err instanceof Error ? err.message : t("aiStudio.error.projects")))
      .finally(() => setIsLoadingProjects(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!selectedProjectId) return;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- fetch-on-mount pattern; the page is a server-side wrapper for a client list, no Suspense boundary at this level yet.
    setIsLoadingModels(true);
    setError(null);
    setWarning(null);
    mlflowApi
      .listRegisteredModels(selectedProjectId)
      .then((data) => {
        setModels(data.models ?? []);
        setWarning(data.warning ?? null);
      })
      .catch((err) => setError(err instanceof Error ? err.message : t("aiStudio.error.mlflow")))
      .finally(() => setIsLoadingModels(false));
  }, [selectedProjectId]);

  function handleProjectChange(id: string) {
    setSelectedProjectId(id);
  }

  if (isLoadingProjects) {
    return <div className="flex h-64 items-center justify-center"><Spinner size="lg" /></div>;
  }

  return (
    <div>
      <div className="mb-8">
        <Breadcrumb
          items={[
            { label: t("common.crumb.console"), href: "/projects" },
            { label: t("aiStudio.crumb.aiStudio") },
            { label: t("aiStudio.crumb.registry") },
          ]}
        />
        <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("aiStudio.title")}</h1>
        <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("aiStudio.subtitle")}</p>
      </div>

      {projects.length > 1 && (
        <div className="mb-4 flex items-center gap-3">
          <label className="text-sm font-medium text-gray-700 dark:text-gray-200">{t("aiStudio.project.label")}</label>
          <select
            value={selectedProjectId}
            onChange={(e) => handleProjectChange(e.target.value)}
            className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          >
            {projects.map((p) => (
              <option key={p.id} value={p.id}>{p.display_name}</option>
            ))}
          </select>
        </div>
      )}

      {warning && (
        <div className="mb-4 rounded-lg border border-yellow-200 dark:border-yellow-900 bg-yellow-50 dark:bg-yellow-950/40 px-4 py-3 text-sm text-yellow-800 dark:text-yellow-300">
          {warning}
        </div>
      )}
      {error && (
        <div className="mb-4 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-400">
          {error}
        </div>
      )}

      <DataTable<MLflowRegisteredModel>
        loading={isLoadingModels}
        rows={models}
        getRowKey={(m) => m.name}
        searchText={(m) => `${m.name} ${m.latest_versions?.[0]?.current_stage ?? ""}`}
        searchPlaceholder={t("aiStudio.search.placeholder")}
        columns={registryColumns(selectedProjectId, t)}
        emptyState={
          <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 py-16">
            <p className="text-sm font-medium text-gray-500 dark:text-gray-400">{t("aiStudio.empty.title")}</p>
            <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">
              {t("aiStudio.empty.hint")} <span className="font-mono">{getProjectPrefixHint(projects, selectedProjectId)}</span>
            </p>
          </div>
        }
      />
    </div>
  );
}

function registryColumns(projectId: string, t: (key: string, vars?: Record<string, string | number>) => string): Column<MLflowRegisteredModel>[] {
  return [
    {
      key: "name",
      header: t("aiStudio.col.name"),
      sortValue: (m) => m.name,
      render: (m) => <span className="font-mono text-gray-900 dark:text-gray-100">{m.name}</span>,
    },
    {
      key: "version",
      header: t("aiStudio.col.version"),
      render: (m) => <span className="font-mono">v{m.latest_versions?.[0]?.version ?? "—"}</span>,
    },
    {
      key: "stage",
      header: t("aiStudio.col.stage"),
      sortValue: (m) => m.latest_versions?.[0]?.current_stage ?? "",
      render: (m) => m.latest_versions?.[0]?.current_stage ?? "—",
    },
    {
      key: "updated",
      header: t("aiStudio.col.updated"),
      sortValue: (m) => m.last_updated_timestamp ?? 0,
      render: (m) => <span className="text-xs text-gray-400 dark:text-gray-500">{fmtTimestamp(m.last_updated_timestamp)}</span>,
    },
    {
      key: "action",
      header: t("aiStudio.col.action"),
      align: "right",
      render: (m) => {
        const latest = m.latest_versions?.[0];
        return (
          <Link
            href={`/projects/${encodeURIComponent(projectId)}/models?fromMlflow=${encodeURIComponent(m.name)}${latest ? `&fromMlflowVersion=${encodeURIComponent(latest.version)}` : ""}`}
            className="inline-flex items-center gap-1 rounded-lg bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700 transition-colors"
          >
            {t("aiStudio.deploy", { version: latest?.version ?? "?" })}
          </Link>
        );
      },
    },
  ];
}

function getProjectPrefixHint(projects: Project[], id: string): string {
  const p = projects.find((x) => x.id === id);
  return p ? `s3://platform-models/${p.name}/...` : "your project's prefix";
}

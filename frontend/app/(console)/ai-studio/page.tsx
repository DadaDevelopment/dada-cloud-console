"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import { projectsApi, mlflowApi } from "@/lib/api";
import type { Project, MLflowRegisteredModel } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { DataTable, type Column } from "@/components/ui/data-table";

function fmtTimestamp(ms?: number): string {
  if (!ms) return "—";
  return new Date(ms).toLocaleString();
}

export default function AIStudioRegistryPage() {
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
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load projects"))
      .finally(() => setIsLoadingProjects(false));
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
      .catch((err) => setError(err instanceof Error ? err.message : "MLflow registry unreachable"))
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
            { label: "Console", href: "/projects" },
            { label: "AI Studio" },
            { label: "Registry" },
          ]}
        />
        <h1 className="mt-2 text-2xl font-bold text-gray-900">MLflow registry</h1>
        <p className="mt-0.5 text-sm text-gray-500">
          Browse registered models filtered by your project&apos;s storage prefix. Click any row to deploy that version.
        </p>
      </div>

      {projects.length > 1 && (
        <div className="mb-4 flex items-center gap-3">
          <label className="text-sm font-medium text-gray-700">Project:</label>
          <select
            value={selectedProjectId}
            onChange={(e) => handleProjectChange(e.target.value)}
            className="rounded-lg border border-gray-300 px-3 py-1.5 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          >
            {projects.map((p) => (
              <option key={p.id} value={p.id}>{p.display_name}</option>
            ))}
          </select>
        </div>
      )}

      {warning && (
        <div className="mb-4 rounded-lg border border-yellow-200 bg-yellow-50 px-4 py-3 text-sm text-yellow-800">
          {warning}
        </div>
      )}
      {error && (
        <div className="mb-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          {error}
        </div>
      )}

      <DataTable<MLflowRegisteredModel>
        loading={isLoadingModels}
        rows={models}
        getRowKey={(m) => m.name}
        searchText={(m) => `${m.name} ${m.latest_versions?.[0]?.current_stage ?? ""}`}
        searchPlaceholder="Search models…"
        columns={registryColumns(selectedProjectId)}
        emptyState={
          <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-gray-50 py-16">
            <p className="text-sm font-medium text-gray-500">No registered models match this project&apos;s storage prefix</p>
            <p className="mt-1 text-xs text-gray-400">
              Register a model in MLflow whose source URI starts with <span className="font-mono">{getProjectPrefixHint(projects, selectedProjectId)}</span>
            </p>
          </div>
        }
      />
    </div>
  );
}

function registryColumns(projectId: string): Column<MLflowRegisteredModel>[] {
  return [
    {
      key: "name",
      header: "Name",
      sortValue: (m) => m.name,
      render: (m) => <span className="font-mono text-gray-900">{m.name}</span>,
    },
    {
      key: "version",
      header: "Latest version",
      render: (m) => <span className="font-mono">v{m.latest_versions?.[0]?.version ?? "—"}</span>,
    },
    {
      key: "stage",
      header: "Stage",
      sortValue: (m) => m.latest_versions?.[0]?.current_stage ?? "",
      render: (m) => m.latest_versions?.[0]?.current_stage ?? "—",
    },
    {
      key: "updated",
      header: "Last updated",
      sortValue: (m) => m.last_updated_timestamp ?? 0,
      render: (m) => <span className="text-xs text-gray-400">{fmtTimestamp(m.last_updated_timestamp)}</span>,
    },
    {
      key: "action",
      header: "Action",
      align: "right",
      render: (m) => {
        const latest = m.latest_versions?.[0];
        return (
          <Link
            href={`/projects/${projectId}/models?fromMlflow=${encodeURIComponent(m.name)}${latest ? `&fromMlflowVersion=${encodeURIComponent(latest.version)}` : ""}`}
            className="inline-flex items-center gap-1 rounded-lg bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700 transition-colors"
          >
            Deploy v{latest?.version ?? "?"} →
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

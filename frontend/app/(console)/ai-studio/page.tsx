"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import { projectsApi, mlflowApi } from "@/lib/api";
import type { Project, MLflowRegisteredModel } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";

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
        <div className="flex items-center gap-2 text-sm text-gray-500">
          <Link href="/projects" className="hover:text-gray-700">Console</Link>
          <span>/</span>
          <span className="text-gray-900">AI Studio</span>
          <span>/</span>
          <span className="text-gray-900">Registry</span>
        </div>
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

      {isLoadingModels ? (
        <div className="flex h-40 items-center justify-center"><Spinner /></div>
      ) : models.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-gray-50 py-16">
          <p className="text-sm font-medium text-gray-500">No registered models match this project&apos;s storage prefix</p>
          <p className="mt-1 text-xs text-gray-400">
            Register a model in MLflow whose source URI starts with <span className="font-mono">{getProjectPrefixHint(projects, selectedProjectId)}</span>
          </p>
        </div>
      ) : (
        <div className="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm">
          <table className="min-w-full divide-y divide-gray-200">
            <thead className="bg-gray-50">
              <tr>
                <Th>Name</Th>
                <Th>Latest version</Th>
                <Th>Stage</Th>
                <Th>Last updated</Th>
                <Th right>Action</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {models.map((m) => {
                const latest = m.latest_versions?.[0];
                return (
                  <tr key={m.name} className="hover:bg-gray-50">
                    <td className="px-5 py-3 font-mono text-sm text-gray-900">{m.name}</td>
                    <td className="px-5 py-3 font-mono text-sm text-gray-700">v{latest?.version ?? "—"}</td>
                    <td className="px-5 py-3 text-sm text-gray-600">{latest?.current_stage ?? "—"}</td>
                    <td className="px-5 py-3 text-xs text-gray-400">{fmtTimestamp(m.last_updated_timestamp)}</td>
                    <td className="px-5 py-3 text-right">
                      <Link
                        href={`/projects/${selectedProjectId}/models?fromMlflow=${encodeURIComponent(m.name)}${latest ? `&fromMlflowVersion=${encodeURIComponent(latest.version)}` : ""}`}
                        className="inline-flex items-center gap-1 rounded-lg bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700 transition-colors"
                      >
                        Deploy v{latest?.version ?? "?"} →
                      </Link>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function getProjectPrefixHint(projects: Project[], id: string): string {
  const p = projects.find((x) => x.id === id);
  return p ? `s3://platform-models/${p.name}/...` : "your project's prefix";
}

function Th({ children, right }: { children: React.ReactNode; right?: boolean }) {
  return (
    <th className={`px-5 py-3 text-xs font-semibold uppercase tracking-wide text-gray-500 ${right ? "text-right" : "text-left"}`}>
      {children}
    </th>
  );
}

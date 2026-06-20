"use client";
import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { appsApi } from "@/lib/api";
import type { ResourceSnapshot, AppSummary } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { PhaseBadge } from "@/components/ui/phase-badge";

export default function DeploymentsPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;

  const { project, selectedEnv, loading: isLoadingEnvs } = useProjectContext();
  const selectedEnvId = selectedEnv?.id ?? "";

  const [apps, setApps] = useState<ResourceSnapshot[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    /* eslint-disable react-hooks/set-state-in-effect */
    if (!selectedEnvId) {
      if (!isLoadingEnvs) setLoading(false);
      return;
    }
    setLoading(true);
    setError(null);
    /* eslint-enable react-hooks/set-state-in-effect */
    appsApi
      .list(projectId, selectedEnvId)
      .then((d) => setApps(d.apps ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load applications"))
      .finally(() => setLoading(false));
  }, [projectId, selectedEnvId, isLoadingEnvs]);

  if (isLoadingEnvs) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner size="lg" />
      </div>
    );
  }

  return (
    <div>
      <div className="mb-8">
        <Breadcrumb
          items={[
            { label: "Projects", href: "/projects" },
            { label: project?.display_name ?? "Overview", href: `/projects/${projectId}` },
            { label: "Deployments" },
          ]}
        />
        <h1 className="mt-2 text-2xl font-bold text-gray-900">Deployments</h1>
        <p className="mt-0.5 text-sm text-gray-500">Pick an application to view its builds, deployments and rollbacks</p>
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{error}</div>
      )}

      {loading ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : apps.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-gray-50 py-16">
          <p className="text-sm font-medium text-gray-500">No applications in {selectedEnv?.name ?? "this environment"}</p>
          <Link href={`/projects/${projectId}/apps`} className="mt-3 text-sm text-blue-600 hover:text-blue-700">
            Create an application →
          </Link>
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {apps.map((app) => {
            const summary = app.summary_json as unknown as AppSummary;
            return (
              <Link
                key={app.id}
                href={`/projects/${projectId}/apps/${app.name}/deployments?envId=${selectedEnvId}`}
                className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm transition-all hover:border-blue-200 hover:shadow-md"
              >
                <div className="mb-3 flex items-start justify-between">
                  <div className="min-w-0 flex-1">
                    <p className="font-mono text-sm font-semibold text-gray-900">{app.name}</p>
                    <p className="mt-0.5 truncate font-mono text-xs text-gray-400">{summary.image ?? "—"}</p>
                  </div>
                  <PhaseBadge phase={app.phase} />
                </div>
                <p className="text-sm font-medium text-blue-600">View deployments →</p>
              </Link>
            );
          })}
        </div>
      )}
    </div>
  );
}

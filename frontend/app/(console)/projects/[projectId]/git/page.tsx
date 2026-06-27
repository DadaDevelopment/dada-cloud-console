"use client";
import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { gitApi } from "@/lib/api";
import type { GitRepo } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { timeAgo } from "@/lib/format";
import { useT } from "@/lib/i18n/console/context";

export default function GitPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const { t } = useT();

  const { project, selectedEnv, role, loading: isLoadingEnvs } = useProjectContext();
  const selectedEnvId = selectedEnv?.id ?? "";

  const [repos, setRepos] = useState<GitRepo[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    /* eslint-disable react-hooks/set-state-in-effect */
    if (!selectedEnvId) {
      if (!isLoadingEnvs) setIsLoading(false);
      return;
    }
    setIsLoading(true);
    setError(null);
    /* eslint-enable react-hooks/set-state-in-effect */
    gitApi
      .listRepos(projectId, selectedEnvId)
      .then((d) => setRepos(d.repos ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : t("git.error.load")))
      .finally(() => setIsLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, selectedEnvId, isLoadingEnvs]);

  const canConnect = canMutate(role);

  if (isLoadingEnvs) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner size="lg" />
      </div>
    );
  }

  return (
    <div>
      <div className="mb-8 flex items-start justify-between">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.projects"), href: "/projects" },
              { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
              { label: t("nav.git") },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900">{t("git.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500">{t("git.subtitle")}</p>
        </div>
        {canConnect && (
          <Link
            href={`/projects/${projectId}/git/import${selectedEnvId ? `?envId=${selectedEnvId}` : ""}`}
            className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 transition-colors"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            {t("git.importRepo")}
          </Link>
        )}
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{error}</div>
      )}

      {isLoading ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : repos.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-gray-50 py-16">
          <svg className="mb-3 h-12 w-12 text-gray-300" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9.568 3H5.25A2.25 2.25 0 003 5.25v4.318c0 .597.237 1.17.659 1.591l9.581 9.581c.699.699 1.78.872 2.607.33a18.095 18.095 0 005.223-5.223c.542-.827.369-1.908-.33-2.607L11.16 3.66A2.25 2.25 0 009.568 3z" />
          </svg>
          <p className="text-sm font-medium text-gray-500">
            {t("git.empty.title", { env: selectedEnv?.name ?? "this environment" })}
          </p>
          {canConnect && (
            <Link
              href={`/projects/${projectId}/git/import${selectedEnvId ? `?envId=${selectedEnvId}` : ""}`}
              className="mt-4 text-sm text-blue-600 hover:text-blue-700"
            >
              {t("git.empty.connect")}
            </Link>
          )}
          <p className="mt-3 max-w-sm text-center text-xs text-gray-400">
            {t("git.empty.hint")}
          </p>
        </div>
      ) : (
        <div className="space-y-3">
          {repos.map((repo) => (
            <div key={repo.id} className="flex items-center justify-between rounded-xl border border-gray-200 bg-white px-5 py-4 shadow-sm">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className="inline-flex items-center rounded-full bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-600">
                    {repo.provider}
                  </span>
                  <p className="truncate font-mono text-sm font-semibold text-gray-900">{repo.repo_full_name}</p>
                </div>
                <p className="mt-1 text-xs text-gray-400">
                  {t("git.repo.app", { name: repo.app_name })} · {t("git.repo.branch", { name: repo.production_branch })}
                  {repo.root_dir && repo.root_dir !== "." && (
                    <> · {t("git.repo.root", { path: repo.root_dir })}</>
                  )}
                  {repo.framework_override && <> · {repo.framework_override}</>}
                </p>
              </div>
              <div className="flex items-center gap-4">
                {repo.auto_deploy && (
                  <span className="inline-flex items-center rounded-full bg-green-50 px-2 py-0.5 text-xs font-medium text-green-700 ring-1 ring-green-600/20">
                    {t("git.repo.autoDeploy")}
                  </span>
                )}
                <Link
                  href={`/projects/${projectId}/apps/${repo.app_name}/deployments${selectedEnvId ? `?envId=${selectedEnvId}` : ""}`}
                  className="text-sm font-medium text-blue-600 hover:text-blue-700"
                >
                  {t("git.repo.viewBuilds")}
                </Link>
              </div>
            </div>
          ))}
          <p className="pt-1 text-xs text-gray-400">
            {repos.length > 0 ? t("git.repo.connected", { ago: timeAgo(repos[0].updated_at) }) : ""}
          </p>
        </div>
      )}
    </div>
  );
}

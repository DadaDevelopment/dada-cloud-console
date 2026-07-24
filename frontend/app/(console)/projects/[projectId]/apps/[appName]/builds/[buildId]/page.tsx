"use client";
import { useCallback, useEffect, useState } from "react";
import { useParams, useSearchParams } from "next/navigation";
import { buildsApi } from "@/lib/api";
import type { Build } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { timeAgo } from "@/lib/format";
import { BuildStatusBadge, isBuildActive } from "@/components/deploy/build-status-badge";
import { BuildLogViewer } from "@/components/deploy/build-log-viewer";
import { useT } from "@/lib/i18n/console/context";

export default function BuildDetailPage() {
  const params = useParams<{ projectId: string; appName: string; buildId: string }>();
  const { projectId, appName, buildId } = params;
  const searchParams = useSearchParams();
  const { selectedEnv, role } = useProjectContext();
  const envId = searchParams.get("envId") || selectedEnv?.id || "";
  const { t } = useT();

  const [build, setBuild] = useState<Build | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [canceling, setCanceling] = useState(false);

  const canDeploy = canMutate(role);

  const load = useCallback(
    async (silent = false) => {
      if (!silent) setLoading(true);
      try {
        const { build: b } = await buildsApi.get(projectId, buildId);
        setBuild(b);
        setError(null);
      } catch (err) {
        setError(err instanceof Error ? err.message : t("apps.builds.error.load"));
      } finally {
        if (!silent) setLoading(false);
      }
    },
    [projectId, buildId]
  );

  useEffect(() => {
    void load(); // eslint-disable-line react-hooks/set-state-in-effect
  }, [load]);

  // Poll status while the build is in-progress so the badge/transitions update.
  useEffect(() => {
    if (!build || !isBuildActive(build.status)) return;
    const interval = setInterval(() => void load(true), 3000);
    return () => clearInterval(interval);
  }, [build, load]);

  async function handleCancel() {
    setCanceling(true);
    try {
      await buildsApi.cancel(projectId, buildId);
      await load(true);
    } catch (err) {
      const msg = err instanceof Error ? err.message : t("apps.builds.error.cancel");
      setError(/409|not cancellable/i.test(msg) ? t("apps.builds.error.notCancelable") : msg);
    } finally {
      setCanceling(false);
    }
  }

  return (
    <div>
      <Breadcrumb
        items={[
          { label: t("common.crumb.projects"), href: "/projects" },
          { label: t("common.crumb.overview"), href: `/projects/${projectId}` },
          { label: t("nav.apps"), href: `/projects/${projectId}/apps` },
          { label: appName, href: `/projects/${projectId}/apps/${appName}${envId ? `?envId=${envId}` : ""}` },
          { label: t("apps.builds.crumb.deployments"), href: `/projects/${projectId}/apps/${appName}/deployments${envId ? `?envId=${envId}` : ""}` },
          { label: t("apps.builds.crumb.build", { id: buildId.slice(0, 7) }) },
        ]}
      />

      {loading ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : error && !build ? (
        <div className="mt-4 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">{error}</div>
      ) : build ? (
        <>
          <div className="mb-6 mt-2 flex items-start justify-between">
            <div>
              <div className="flex items-center gap-3">
                <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">{t("apps.builds.heading")}</h1>
                <BuildStatusBadge status={build.status} />
              </div>
              <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
                <span className="font-mono">{build.commit_sha?.slice(0, 7) ?? "—"}</span> {t("apps.builds.meta.on")}{" "}
                <span className="font-mono">{build.branch}</span> · {build.trigger} · {timeAgo(build.created_at)}
              </p>
              {build.commit_message && <p className="mt-1 text-sm text-gray-700 dark:text-gray-200">{build.commit_message}</p>}
              {build.image_uri && (
                <p className="mt-1 truncate font-mono text-xs text-gray-400 dark:text-gray-500">→ {build.image_uri}</p>
              )}
            </div>
            {canDeploy && isBuildActive(build.status) && (
              <button
                onClick={handleCancel}
                disabled={canceling}
                className="rounded-lg border border-gray-300 dark:border-gray-700 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50 transition-colors"
              >
                {canceling ? t("apps.builds.canceling") : t("apps.builds.cancel")}
              </button>
            )}
          </div>

          {error && (
            <div className="mb-4 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">{error}</div>
          )}

          {build.status === "failed" && (build.fail_reason || build.error_message) && (
            <div className="mb-4 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
              <p className="font-medium">{t("apps.builds.fail.heading")}</p>
              {build.fail_reason === "no_dockerfile" ? (
                <p className="mt-1">{t("apps.builds.fail.noDockerfile.hint")}</p>
              ) : build.error_message ? (
                <p className="mt-1 whitespace-pre-wrap break-words font-mono text-xs">{build.error_message}</p>
              ) : null}
            </div>
          )}

          <BuildLogViewer projectId={projectId} buildId={buildId} />
        </>
      ) : null}
    </div>
  );
}

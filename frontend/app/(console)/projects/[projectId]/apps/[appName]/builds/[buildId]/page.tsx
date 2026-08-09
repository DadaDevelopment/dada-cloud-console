"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useParams, useSearchParams, useRouter } from "next/navigation";
import { buildsApi, appsApi } from "@/lib/api";
import type { Build } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { timeAgo } from "@/lib/format";
import { BuildStatusBadge, isBuildActive } from "@/components/deploy/build-status-badge";
import { BuildLogViewer } from "@/components/deploy/build-log-viewer";
import { useT } from "@/lib/i18n/console/context";
import { trackUxEvent } from "@/lib/ux-telemetry";
import { formatCommitLabel, resolveCommit } from "@/lib/build-commit";
import { trackBuildStart } from "@/lib/build-watch";
import { buildFailureDetail } from "@/lib/build-failure";

const PYTHON_BOT_DOCKERFILE = `FROM python:3.12-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY . .
CMD ["python", "main.py"]`;

const appReadyPollMs = 5000;
const appReadyMaxPolls = 60;

export default function BuildDetailPage() {
  const params = useParams<{ projectId: string; appName: string; buildId: string }>();
  const { projectId, appName, buildId } = params;
  const searchParams = useSearchParams();
  const router = useRouter();
  const { selectedEnv, role } = useProjectContext();
  const envId = searchParams.get("envId") || selectedEnv?.id || "";
  const { t } = useT();

  const [build, setBuild] = useState<Build | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [canceling, setCanceling] = useState(false);
  const [rebuilding, setRebuilding] = useState(false);
  const [appUrl, setAppUrl] = useState<string | null>(null);
  const [appReady, setAppReady] = useState(false);
  const successViewedRef = useRef(false);
  const readyCtaViewedRef = useRef(false);
  const appUrlPollsRef = useRef(0);

  const canDeploy = canMutate(role);
  const failureDetail = buildFailureDetail(build?.fail_reason, build?.error_message);

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

  /**
   * Reports one `view` event for the success panel, guarded so the 3s poll
   * and re-renders after it don't multiply rows. Same key family as the
   * `open_app` click (build_success_cta:*) so a show->click conversion can
   * be computed the way app-next-step-card.tsx already does.
   */
  useEffect(() => {
    if (build?.status !== "success") return;
    if (successViewedRef.current) return;
    successViewedRef.current = true;
    trackUxEvent("view", "build_success_cta:panel");
  }, [build?.status]);

  /**
   * Fetches the app's live URL and phase once the build succeeds. A rollout
   * lags its build, so this polls until the app reports `ready` rather than
   * reading the phase once: the URL-less panel promises the link will appear
   * on its own, and a one-shot read would make that promise false until a
   * manual reload. Polling stops on ready and is capped so a permanently
   * crashing app cannot poll forever. A failure here must never surface as
   * an error -- the build itself succeeded -- so the catch is silent and the
   * panel just falls back to its URL-less layout.
   */
  useEffect(() => {
    if (build?.status !== "success") return;
    if (!envId) return;
    if (appReady) return;
    let canceled = false;
    const read = () => {
      if (appUrlPollsRef.current >= appReadyMaxPolls) return;
      appUrlPollsRef.current += 1;
      appsApi
        .list(projectId, envId)
        .then((data) => {
          if (canceled) return;
          const found = (data.apps ?? []).find((a) => a.name === appName);
          const summary = found?.summary_json as { url?: string } | undefined;
          if (summary?.url) setAppUrl(summary.url);
          setAppReady((found?.phase ?? "").toLowerCase() === "ready");
        })
        .catch(() => {});
    };
    read();
    const timer = setInterval(read, appReadyPollMs);
    return () => {
      canceled = true;
      clearInterval(timer);
    };
  }, [build?.status, envId, projectId, appName, appReady]);

  /**
   * Same gate as `app-latest-build-card.tsx`: a build can succeed while the
   * rollout it produced is still crashing, so "Open application" must wait
   * for the app's own phase, not just the build outcome (see 9fbee62). The
   * `app_ready_cta:panel` view is guarded so the 3s poll never inflates it,
   * and shares the key family with the other panel so a show->click
   * conversion can be read across both surfaces.
   */
  useEffect(() => {
    if (build?.status !== "success") return;
    if (!appReady || !appUrl) return;
    if (readyCtaViewedRef.current) return;
    readyCtaViewedRef.current = true;
    trackUxEvent("view", "app_ready_cta:panel");
  }, [build?.status, appReady, appUrl]);

  async function handleRebuild() {
    if (!envId || !build) return;
    setRebuilding(true);
    setError(null);
    try {
      const { build: newBuild } = await buildsApi.trigger(projectId, envId, appName);
      if (newBuild?.id) {
        trackBuildStart({ projectId, envId, appName, buildId: newBuild.id });
        router.push(`/projects/${projectId}/apps/${appName}/builds/${newBuild.id}${envId ? `?envId=${envId}` : ""}`);
        return;
      }
      await load(true);
    } catch (err) {
      const msg = err instanceof Error ? err.message : t("apps.builds.error.rebuild");
      setError(/409|not connected/i.test(msg) ? t("apps.deployments.error.noRepo") : msg);
    } finally {
      setRebuilding(false);
    }
  }

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
                {(() => {
                  const resolved = resolveCommit(build);
                  if (resolved.kind === "sha") {
                    return (
                      <>
                        <span className="font-mono">{resolved.sha.slice(0, 7)}</span> {t("apps.builds.meta.on")}{" "}
                        <span className="font-mono">{build.branch}</span>
                      </>
                    );
                  }
                  if (resolved.kind === "branch") {
                    return <span>{t("common.commit.branchLatest", { branch: resolved.branch })}</span>;
                  }
                  if (resolved.kind === "archive") {
                    return <span>{formatCommitLabel(resolved, t)}</span>;
                  }
                  return <span>{t("common.commit.archive")}</span>;
                })()}{" "}
                · {build.trigger} · {timeAgo(build.created_at)}
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
            {canDeploy && (build.status === "failed" || build.status === "canceled") && envId && (
              <button
                onClick={handleRebuild}
                disabled={rebuilding}
                data-ux="apps_build_detail:rebuild"
                className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50 transition-colors"
              >
                {rebuilding ? t("apps.builds.rebuilding") : t("apps.builds.rebuild")}
              </button>
            )}
          </div>

          {error && (
            <div className="mb-4 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">{error}</div>
          )}

          {build.status === "success" && (
            <div className="mb-4 flex items-center justify-between gap-4 rounded-lg border border-green-200 dark:border-green-900 bg-green-50 dark:bg-green-950/40 px-4 py-3 text-sm text-green-700 dark:text-green-300">
              <div className="min-w-0">
                <p className="font-medium">{t("apps.builds.success.heading")}</p>
                {appUrl && appReady && (
                  <a
                    href={appUrl}
                    target="_blank"
                    rel="noopener noreferrer"
                    data-ux="build_success_cta:open_url"
                    className="mt-1 block truncate font-mono text-xs underline underline-offset-2 hover:text-green-800 dark:hover:text-green-200"
                  >
                    {appUrl}
                  </a>
                )}
                {appUrl && !appReady && (
                  <p className="mt-1 text-xs text-green-600 dark:text-green-400">{t("apps.builds.success.notReady")}</p>
                )}
              </div>
              <Link
                href={`/projects/${projectId}/apps/${appName}${envId ? `?envId=${envId}` : ""}`}
                data-ux="build_success_cta:open_app"
                className="shrink-0 rounded-lg bg-green-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-green-700"
              >
                {t("apps.builds.success.openApp")}
              </Link>
            </div>
          )}

          {build.status === "failed" && (build.fail_reason || build.error_message) && (
            <div className="mb-4 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
              <p className="font-medium">{t("apps.builds.fail.heading")}</p>
              {build.fail_reason === "no_dockerfile" ? (
                <div className="mt-1 space-y-2">
                  <p>{t("apps.builds.fail.noDockerfile.hint")}</p>
                  <p>{t("apps.builds.fail.noDockerfile.botLead")}</p>
                  <pre className="overflow-x-auto rounded-md bg-red-100 dark:bg-red-900/40 px-3 py-2 font-mono text-xs">{PYTHON_BOT_DOCKERFILE}</pre>
                  <p>
                    {t("apps.builds.fail.noDockerfile.botCmdNote")}{" "}
                    <a
                      href="https://cloud.dada-tuda.ru/hosting-telegram-bot?utm_source=console_build_fail"
                      target="_blank"
                      rel="noopener noreferrer"
                      className="font-medium underline underline-offset-2"
                    >
                      {t("apps.builds.fail.noDockerfile.botGuide")}
                    </a>
                  </p>
                </div>
              ) : build.fail_reason === "git_auth_failed" ? (
                <div className="mt-1 space-y-2">
                  <p>{t("apps.builds.fail.reason.gitAuthFailed")}</p>
                  <Link
                    href={`/projects/${projectId}/apps/${appName}/settings${envId ? `?envId=${envId}` : ""}`}
                    data-ux="build_fail_cta:reconnect_repo"
                    className="inline-block font-medium underline underline-offset-2"
                  >
                    {t("apps.builds.fail.gitAuth.reconnect")}
                  </Link>
                  {failureDetail ? (
                    <p className="whitespace-pre-wrap break-words font-mono text-xs opacity-80">{failureDetail}</p>
                  ) : null}
                </div>
              ) : build.fail_reason === "platform_error" ? (
                <div className="mt-1 space-y-2">
                  <p>{t("apps.builds.fail.reason.platformError")}</p>
                  {failureDetail ? (
                    <p className="whitespace-pre-wrap break-words font-mono text-xs opacity-80">{failureDetail}</p>
                  ) : null}
                </div>
              ) : build.fail_reason === "dockerfile_build_failed" || build.fail_reason === "build_failed" ? (
                <div className="mt-1 space-y-2">
                  <p>
                    {build.fail_reason === "dockerfile_build_failed"
                      ? t("apps.builds.fail.reason.dockerfileBuildFailed")
                      : t("apps.builds.fail.reason.buildFailed")}
                  </p>
                  {failureDetail ? (
                    <p className="whitespace-pre-wrap break-words font-mono text-xs opacity-80">{failureDetail}</p>
                  ) : null}
                </div>
              ) : failureDetail ? (
                <p className="mt-1 whitespace-pre-wrap break-words font-mono text-xs">{failureDetail}</p>
              ) : null}
            </div>
          )}

          <BuildLogViewer
            projectId={projectId}
            buildId={buildId}
            onCancel={canDeploy && isBuildActive(build.status) ? handleCancel : undefined}
            canceling={canceling}
            cancelLabel={t("apps.builds.cancel")}
            cancelingLabel={t("apps.builds.canceling")}
          />
        </>
      ) : null}
    </div>
  );
}

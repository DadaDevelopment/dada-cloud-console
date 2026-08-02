"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { ExternalLink, Rocket } from "lucide-react";
import { buildsApi } from "@/lib/api";
import type { Build } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { BuildStatusBadge, isBuildActive } from "@/components/deploy/build-status-badge";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { useT } from "@/lib/i18n/console/context";
import { trackUxEvent } from "@/lib/ux-telemetry";

const POLL_MS = 3000;

interface AppLatestBuildCardProps {
  projectId: string;
  envId: string;
  appName: string;
  appUrl?: string;
  appReady: boolean;
  buildHref: (buildId: string) => string;
}

/**
 * Surfaces the app's most recent build directly on the app page, where the
 * user's eyes actually are (pageview data showed zero visits to /builds* and
 * /deployments* while builds kept succeeding). Polls `buildsApi.list` every
 * `POLL_MS` only while the latest build is still in flight; once it settles
 * the poll stops so this never runs alongside the page's own app poll for
 * no reason.
 *
 * The "open the live app" CTA additionally requires `appReady` (the app's own
 * phase is `Ready`, not just "the build that produced this image succeeded"):
 * a build can succeed and the rollout can still be crashing, in which case
 * the URL has nothing to answer yet. The `app_ready_cta:panel` view is
 * guarded per build id so polling never inflates it, mirroring the pattern
 * used for `BuildViewKey` below.
 */
export function AppLatestBuildCard({ projectId, envId, appName, appUrl, appReady, buildHref }: AppLatestBuildCardProps) {
  const { t } = useT();
  const router = useRouter();
  const { role } = useProjectContext();
  const canDeploy = canMutate(role) && Boolean(envId);
  const [build, setBuild] = useState<Build | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [rebuilding, setRebuilding] = useState(false);
  const [rebuildError, setRebuildError] = useState<string | null>(null);
  const viewedRef = useRef<BuildViewKey | null>(null);
  const readyCtaViewedRef = useRef<string | null>(null);

  useEffect(() => {
    if (!envId) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const poll = () => {
      buildsApi
        .list(projectId, envId, appName)
        .then((data) => {
          if (cancelled) return;
          const latest = (data.builds ?? [])[0] ?? null;
          setBuild(latest);
          setLoaded(true);
          if (latest && isBuildActive(latest.status)) {
            timer = setTimeout(poll, POLL_MS);
          }
        })
        .catch(() => {
          if (cancelled) return;
          setLoaded(true);
        });
    };
    poll();

    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [projectId, envId, appName]);

  useEffect(() => {
    if (!build) return;
    const key = viewKeyFor(build);
    if (!key || viewedRef.current === key) return;
    viewedRef.current = key;
    trackUxEvent("view", `app_latest_build:${key}`);
  }, [build]);

  useEffect(() => {
    if (!build || build.status !== "success") return;
    if (!appReady || !appUrl) return;
    if (readyCtaViewedRef.current === build.id) return;
    readyCtaViewedRef.current = build.id;
    trackUxEvent("view", "app_ready_cta:panel");
  }, [build, appReady, appUrl]);

  /**
   * Same call as the build detail page's own retry button
   * (builds/[buildId]/page.tsx handleRebuild): trigger a fresh build and
   * jump straight to it. This is the surface users are actually on when a
   * build fails, per the audit_events read that shows /builds* pages
   * getting near-zero traffic while this card gets clicked live.
   */
  async function handleRebuild() {
    if (!envId) return;
    setRebuilding(true);
    setRebuildError(null);
    try {
      const { build: newBuild } = await buildsApi.trigger(projectId, envId, appName);
      if (newBuild?.id) {
        router.push(buildHref(newBuild.id));
        return;
      }
      setRebuildError(t("apps.builds.error.rebuild"));
    } catch (err) {
      const msg = err instanceof Error ? err.message : t("apps.builds.error.rebuild");
      setRebuildError(/409|not connected/i.test(msg) ? t("apps.deployments.error.noRepo") : msg);
    } finally {
      setRebuilding(false);
    }
  }

  if (!loaded || !build) return null;

  if (isBuildActive(build.status)) {
    return (
      <div className="mb-6 flex items-center gap-3 rounded-xl border border-blue-100 dark:border-blue-950 bg-blue-50/50 dark:bg-blue-950/20 px-5 py-4">
        <Spinner size="sm" />
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium text-gray-900 dark:text-gray-100">
            {t("apps.latestBuild.running")} <BuildStatusBadge status={build.status} />
          </p>
        </div>
        <Link
          href={buildHref(build.id)}
          data-ux="app_latest_build:build_logs"
          className="shrink-0 text-sm font-medium text-blue-600 dark:text-blue-400 hover:underline"
        >
          {t("apps.latestBuild.viewLogs")}
        </Link>
      </div>
    );
  }

  if (build.status === "success") {
    return (
      <div className="mb-6 rounded-xl border border-green-100 dark:border-green-950 bg-green-50/50 dark:bg-green-950/20 px-5 py-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="min-w-0">
            <p className="text-sm font-semibold text-green-700 dark:text-green-400">{t("apps.latestBuild.success.heading")}</p>
            <p className="mt-0.5 truncate text-xs text-gray-500 dark:text-gray-400">
              {build.commit_sha && (
                <span className="font-mono">{build.commit_sha.slice(0, 7)}</span>
              )}
              {build.commit_sha && build.branch && " · "}
              {build.branch}
            </p>
            {appUrl && appReady && (
              <a
                href={appUrl}
                target="_blank"
                rel="noopener noreferrer"
                data-ux="app_latest_build:open_url"
                className="mt-1 block truncate text-sm font-mono text-blue-600 dark:text-blue-400 hover:underline"
              >
                {appUrl}
              </a>
            )}
            {appUrl && !appReady && (
              <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{t("apps.latestBuild.success.notReady")}</p>
            )}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <Link
              href={buildHref(build.id)}
              data-ux="app_latest_build:build_logs"
              className="rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-1.5 text-xs font-medium text-gray-700 dark:text-gray-200 hover:border-blue-300 hover:text-blue-600 transition-colors"
            >
              {t("apps.latestBuild.viewLogs")}
            </Link>
            {appUrl && appReady && (
              <a
                href={appUrl}
                target="_blank"
                rel="noopener noreferrer"
                data-ux="app_latest_build:open_app"
                className="inline-flex items-center gap-1.5 rounded-lg bg-green-600 px-3 py-1.5 text-xs font-semibold text-white shadow-sm hover:bg-green-700 transition-colors"
              >
                <ExternalLink className="h-3.5 w-3.5" />
                {t("apps.latestBuild.success.openApp")}
              </a>
            )}
          </div>
        </div>
      </div>
    );
  }

  if (build.status === "failed") {
    const reason = failReasonFor(build, t);
    return (
      <div className="mb-6 rounded-xl border border-red-100 dark:border-red-950 bg-red-50/50 dark:bg-red-950/20 px-5 py-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="min-w-0">
            <p className="text-sm font-semibold text-red-700 dark:text-red-400">{t("apps.latestBuild.failed.heading")}</p>
            <p className="mt-0.5 truncate text-xs text-gray-500 dark:text-gray-400">
              {reason.text}
              {reason.showDetailsLink && (
                <>
                  {" "}
                  <Link href={buildHref(build.id)} data-ux="app_latest_build:fail_details" className="underline hover:text-red-600 dark:hover:text-red-400">
                    {t("apps.builds.fail.reason.detailsLink")}
                  </Link>
                </>
              )}
            </p>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {canDeploy && (
              <button
                type="button"
                onClick={handleRebuild}
                disabled={rebuilding}
                data-ux="app_latest_build:rebuild"
                className="rounded-lg bg-red-600 px-3 py-1.5 text-xs font-semibold text-white shadow-sm hover:bg-red-700 transition-colors disabled:cursor-not-allowed disabled:opacity-60"
              >
                {rebuilding ? t("apps.builds.rebuilding") : t("apps.builds.rebuild")}
              </button>
            )}
            <Link
              href={buildHref(build.id)}
              data-ux="app_latest_build:build_logs"
              className="rounded-lg border border-red-200 dark:border-red-900 bg-white dark:bg-gray-900 px-3 py-1.5 text-xs font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/30 transition-colors"
            >
              {t("apps.latestBuild.viewLogs")}
            </Link>
          </div>
        </div>
        {rebuildError && <p className="mt-2 text-xs text-red-600 dark:text-red-400">{rebuildError}</p>}
      </div>
    );
  }

  return (
    <div className="mb-6 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-4">
      <div className="flex flex-wrap items-center gap-3">
        <Rocket className="h-4 w-4 shrink-0 text-gray-400 dark:text-gray-500" />
        <p className="min-w-0 flex-1 text-sm text-gray-500 dark:text-gray-400">{t("apps.latestBuild.canceled")}</p>
        <div className="flex shrink-0 items-center gap-2">
          {canDeploy && (
            <button
              type="button"
              onClick={handleRebuild}
              disabled={rebuilding}
              data-ux="app_latest_build:rebuild"
              className="rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-1.5 text-xs font-medium text-gray-700 dark:text-gray-200 hover:border-blue-300 hover:text-blue-600 transition-colors disabled:cursor-not-allowed disabled:opacity-60"
            >
              {rebuilding ? t("apps.builds.rebuilding") : t("apps.builds.rebuild")}
            </button>
          )}
          <Link
            href={buildHref(build.id)}
            data-ux="app_latest_build:build_logs"
            className="text-sm font-medium text-blue-600 dark:text-blue-400 hover:underline"
          >
            {t("apps.latestBuild.viewLogs")}
          </Link>
        </div>
      </div>
      {rebuildError && <p className="mt-2 text-xs text-red-600 dark:text-red-400">{rebuildError}</p>}
    </div>
  );
}

type FailReason = { text: string; showDetailsLink?: boolean };

/**
 * Maps the build-agent's machine fail codes (see
 * build-agent/internal/worker/runner.go) to a short human sentence instead
 * of printing the raw code to the user. Falls back to the human-readable
 * `error_message` when there is no code, and to a generic message when
 * neither is present.
 */
function failReasonFor(build: Build, t: ReturnType<typeof useT>["t"]): FailReason {
  switch (build.fail_reason) {
    case "no_dockerfile":
      return { text: t("apps.builds.fail.reason.noDockerfile"), showDetailsLink: true };
    case "dockerfile_build_failed":
      return { text: t("apps.builds.fail.reason.dockerfileBuildFailed") };
    case "git_auth_failed":
      return { text: t("apps.builds.fail.reason.gitAuthFailed") };
    case "build_failed":
      return { text: t("apps.builds.fail.reason.buildFailed") };
    default:
      if (build.error_message) return { text: build.error_message };
      if (build.fail_reason) return { text: t("apps.builds.fail.reason.buildFailed") };
      return { text: t("apps.latestBuild.failed.unknown") };
  }
}

type BuildViewKey = "running" | "success" | "failed" | "canceled";

function viewKeyFor(build: Build): BuildViewKey | null {
  if (isBuildActive(build.status)) return "running";
  if (build.status === "success") return "success";
  if (build.status === "failed") return "failed";
  if (build.status === "canceled") return "canceled";
  return null;
}

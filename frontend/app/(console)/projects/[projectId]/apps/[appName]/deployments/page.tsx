"use client";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useParams, useSearchParams, useRouter } from "next/navigation";
import Link from "next/link";
import { buildsApi, cloudTasksApi, deploymentsApi, gitApi } from "@/lib/api";
import type { Build, Deployment } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { timeAgo } from "@/lib/format";
import { BuildStatusBadge, isBuildActive } from "@/components/deploy/build-status-badge";
import { useT } from "@/lib/i18n/console/context";
import { buildFailureSummary, isRepoFixable } from "@/lib/build-failure";
import { formatCommitLabel, resolveCommit } from "@/lib/build-commit";
import { BuildProvenance, buildTriggerLabel } from "@/components/deploy/build-provenance";
import { trackBuildStart } from "@/lib/build-watch";
import { StarterNextStep } from "@/components/deploy/starter-next-step";
import { isNewestDeployment, isPendingDeployment } from "@/lib/current-deployment";

/**
 * A single row in the unified deploy feed. Either a build attempt (every
 * status, including failed/canceled) optionally carrying the deployment it
 * produced, or an orphan deployment whose originating build row was pruned
 * (e.g. a rollback to an old version). One list, newest first — no separate
 * "builds" vs "deployments" split.
 */
type Row =
  | { kind: "build"; at: string; build: Build; dep?: Deployment }
  | { kind: "deploy"; at: string; dep: Deployment };

export default function AppDeploymentsPage() {
  const params = useParams<{ projectId: string; appName: string }>();
  const { projectId, appName } = params;
  const searchParams = useSearchParams();
  const router = useRouter();
  const { selectedEnv, role } = useProjectContext();
  const envId = searchParams.get("envId") || selectedEnv?.id || "";

  const [builds, setBuilds] = useState<Build[]>([]);
  const [deployments, setDeployments] = useState<Deployment[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const [triggering, setTriggering] = useState(false);
  const [actionId, setActionId] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [repoFullName, setRepoFullName] = useState<string | null>(null);
  const { t } = useT();

  const canDeploy = canMutate(role);

  const load = useCallback(
    async (silent = false) => {
      if (!envId) return;
      if (!silent) setLoading(true);
      try {
        const [b, d] = await Promise.all([
          buildsApi.list(projectId, envId, appName),
          deploymentsApi.list(projectId, envId, appName),
        ]);
        setBuilds(b.builds ?? []);
        setDeployments(d.deployments ?? []);
        setError(null);
        setUnavailable(false);
      } catch (err) {
        const msg = err instanceof Error ? err.message : t("apps.deployments.error.load");
        if (/503|unavailable|not configured/i.test(msg)) setUnavailable(true);
        else setError(msg);
      } finally {
        if (!silent) setLoading(false);
      }
    },
    [projectId, envId, appName]
  );

  useEffect(() => {
    void load(); // eslint-disable-line react-hooks/set-state-in-effect
  }, [load]);

  const latestBuild = useMemo(
    () => [...builds].sort((a, b) => (a.created_at < b.created_at ? 1 : a.created_at > b.created_at ? -1 : 0))[0],
    [builds]
  );

  /**
   * Fetched once (not part of the 3s build poll) purely to detect a
   * starter-template app for the StarterNextStep nudge -- this is literally
   * the page the "deploy a template" redirect lands on, so it is where the
   * dead-end mostly showed up in the psql read. Deliberately gated on a
   * succeeded build: without one the nudge never renders, so an app that has
   * no git repo at all (uploaded archive, still building, failing) never pays
   * for the extra request.
   */
  useEffect(() => {
    if (!envId || latestBuild?.status !== "success") return;
    let cancelled = false;
    gitApi
      .listRepos(projectId, envId)
      .then((data) => {
        if (cancelled) return;
        const repo = (data.repos ?? []).find((r) => r.app_name === appName);
        setRepoFullName(repo?.repo_full_name ?? null);
      })
      .catch(() => {
        if (cancelled) return;
      });
    return () => {
      cancelled = true;
    };
  }, [projectId, envId, appName, latestBuild?.status]);

  /** Poll every 3s while any build is in-progress (mirrors operations/page.tsx). */
  useEffect(() => {
    const hasActive = builds.some((b) => isBuildActive(b.status));
    if (!hasActive) return;
    const interval = setInterval(() => void load(true), 3000);
    return () => clearInterval(interval);
  }, [builds, load]);

  /**
   * Fold builds + deployments into one feed. A deployment attaches to the build
   * that produced it (build_id); deployments with no surviving build become
   * their own rows. Everything is sorted newest-first.
   */
  const rows = useMemo<Row[]>(() => {
    const depByBuild = new Map<string, Deployment>();
    for (const d of deployments) if (d.build_id) depByBuild.set(d.build_id, d);
    const buildIds = new Set(builds.map((b) => b.id));

    const buildRows: Row[] = builds.map((b) => ({
      kind: "build",
      at: b.created_at,
      build: b,
      dep: depByBuild.get(b.id),
    }));
    const orphanRows: Row[] = deployments
      .filter((d) => !d.build_id || !buildIds.has(d.build_id))
      .map((d) => ({ kind: "deploy", at: d.created_at, dep: d }));

    return [...buildRows, ...orphanRows].sort((a, b) => (a.at < b.at ? 1 : a.at > b.at ? -1 : 0));
  }, [builds, deployments]);

  async function handleTrigger() {
    setTriggering(true);
    setNotice(null);
    try {
      const { build } = await buildsApi.trigger(projectId, envId, appName);
      if (build?.id) {
        trackBuildStart({ projectId, envId, appName, buildId: build.id });
        router.push(`/projects/${projectId}/apps/${appName}/builds/${build.id}${envId ? `?envId=${envId}` : ""}`);
        return;
      }
      setNotice(t("apps.deployments.notice.queued", { ref: formatCommitLabel(resolveCommit(build), t) }));
      await load(true);
    } catch (err) {
      const msg = err instanceof Error ? err.message : t("apps.deployments.error.load");
      setError(/409|not connected/i.test(msg) ? t("apps.deployments.error.noRepo") : msg);
    } finally {
      setTriggering(false);
    }
  }

  async function handleCancel(buildId: string) {
    setActionId(buildId);
    try {
      await buildsApi.cancel(projectId, buildId);
      await load(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("apps.deployments.error.cancel"));
    } finally {
      setActionId(null);
    }
  }

  async function handleDeployAction(deploymentId: string, kind: "rollback" | "promote") {
    setActionId(deploymentId);
    setError(null);
    try {
      const res =
        kind === "rollback"
          ? await deploymentsApi.rollback(projectId, deploymentId)
          : await deploymentsApi.promote(projectId, deploymentId);
      const opId = res.operation?.id;
      router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : t(kind === "rollback" ? "apps.deployments.rollingBack" : "apps.deployments.promoting"));
      setActionId(null);
    }
  }

  async function handleAutofix(build: Build) {
    setActionId(build.id);
    setError(null);
    try {
      const resolved = resolveCommit(build);
      const ref = resolved.kind === "sha" ? resolved.sha.slice(0, 12) : formatCommitLabel(resolved, t);
      const summary = buildFailureSummary({
        branch: build.branch,
        commitRef: ref,
        commitMessage: build.commit_message,
        failReason: build.fail_reason,
        errorMessage: build.error_message,
      });
      await cloudTasksApi.triggerAutofix(projectId, envId, appName, summary);
      const envQuery = envId ? `envId=${envId}&` : "";
      router.push(`/projects/${projectId}/apps/${appName}?${envQuery}justRan=autofix#agent`);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("apps.deployments.error.autofix"));
      setActionId(null);
    }
  }

  /**
   * `dep.is_current` lags reality: it flips only once the reconciler has
   * observed the new image running (resource_snapshots), which trails an
   * accepted deploy op by up to a few minutes. During that window, without
   * `isNewest`, the newest deployment -- the one the platform is actively
   * rolling out right now -- read as an ordinary older version with a
   * "Rollback to this" button. That is the megafactory shape: the automatic
   * post-build deploy landed, is_current had not caught up yet, and the user
   * read "Rollback to this" on the freshest row and clicked it, redeploying
   * the exact image that was already on its way. `isNewest` names that row
   * honestly as a redeploy instead of a rollback -- the actual server call is
   * unchanged (still the rollback endpoint), only the label stops lying
   * about direction.
   */
  function DeployActions({ dep, isNewest }: { dep: Deployment; isNewest: boolean }) {
    if (!canDeploy) return null;
    if (dep.is_current) {
      return (
        <button
          onClick={() => handleDeployAction(dep.id, "promote")}
          data-ux="app_deploy_feed:promote"
          disabled={actionId === dep.id}
          className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-xs font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50"
        >
          {actionId === dep.id ? t("apps.deployments.promoting") : t("apps.deployments.promote")}
        </button>
      );
    }
    return (
      <button
        onClick={() => handleDeployAction(dep.id, "rollback")}
        data-ux="app_deploy_feed:rollback"
        disabled={actionId === dep.id}
        className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-xs font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50"
      >
        {actionId === dep.id
          ? t("apps.deployments.rollingBack")
          : isNewest
            ? t("apps.deployments.redeploy")
            : t("apps.deployments.rollback")}
      </button>
    );
  }

  function CurrentBadge() {
    return (
      <span className="inline-flex items-center rounded-full bg-green-100 px-2 py-0.5 text-xs font-semibold text-green-700 dark:bg-green-950/40 dark:text-green-300">
        {t("apps.deployments.badge.current")}
      </span>
    );
  }

  /** The newest deployment before `is_current` has caught up to it -- see DeployActions' doc comment. */
  function PendingBadge() {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-blue-100 px-2 py-0.5 text-xs font-semibold text-blue-700 dark:bg-blue-950/40 dark:text-blue-300">
        <Spinner size="sm" className="h-3 w-3" />
        {t("apps.deployments.badge.pending")}
      </span>
    );
  }

  return (
    <div>
      <div className="mb-8 flex flex-wrap items-start justify-between gap-3">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.projects"), href: "/projects" },
              { label: t("common.crumb.overview"), href: `/projects/${projectId}` },
              { label: t("nav.apps"), href: `/projects/${projectId}/apps` },
              { label: appName, href: `/projects/${projectId}/apps/${appName}${envId ? `?envId=${envId}` : ""}` },
              { label: t("apps.deployments.crumb") },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">
            <span className="font-mono">{appName}</span>
            <span className="ml-2 text-lg font-normal text-gray-400 dark:text-gray-500">{t("apps.deployments.heading.suffix")}</span>
          </h1>
        </div>
        {canDeploy && !unavailable && (
          <div className="flex max-w-sm flex-col items-end gap-1.5">
            <button
              onClick={handleTrigger}
              data-ux="app_deploy_feed:trigger_build"
              disabled={triggering}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50 transition-colors"
            >
              {triggering ? <><Spinner size="sm" /> {t("apps.deployments.queuing")}</> : t("apps.deployments.trigger")}
            </button>
            {latestBuild && <BuildProvenance build={latestBuild} showStatus className="min-w-0" />}
          </div>
        )}
      </div>

      {latestBuild?.status === "success" && (
        <StarterNextStep
          projectId={projectId}
          envId={envId || null}
          repoFullName={repoFullName}
          className="mb-6"
        />
      )}

      {notice && (
        <div className="mb-4 rounded-lg border border-green-200 dark:border-green-900 bg-green-50 dark:bg-green-950/40 px-4 py-3 text-sm text-green-700 dark:text-green-300">{notice}</div>
      )}
      {error && (
        <div className="mb-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">{error}</div>
      )}

      {loading ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : unavailable ? (
        <div className="rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-800 dark:text-amber-200">
          {t("apps.deployments.unavailable")}
          <Link href={`/projects/${projectId}/git${envId ? `?envId=${envId}` : ""}`} className="ml-1 font-medium underline">
            {t("apps.deployments.unavailable.link")}
          </Link>
        </div>
      ) : (
        <section>
          <div className="mb-3">
            <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("apps.deployments.section.deployments")}</h2>
            <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("apps.deployments.section.hint")}</p>
          </div>
          {rows.length === 0 ? (
            <p className="rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 px-5 py-8 text-center text-sm text-gray-500 dark:text-gray-400">
              {t("apps.deployments.empty.feed")}
            </p>
          ) : (
            <div className="space-y-3">
              {rows.map((row) => {
                if (row.kind === "build") {
                  const b = row.build;
                  const dep = row.dep;
                  const isCurrent = dep?.is_current ?? false;
                  const isNewest = dep ? isNewestDeployment(dep.id, deployments) : false;
                  const isPending = dep ? isPendingDeployment(dep.id, deployments) : false;
                  const image = dep?.image_uri ?? b.image_uri;
                  const resolved = resolveCommit(b);
                  return (
                    <div
                      key={b.id}
                      className={`flex items-center justify-between rounded-xl border bg-white dark:bg-gray-900 px-5 py-4 shadow-sm ${
                        isCurrent ? "border-green-300 ring-1 ring-green-200 dark:ring-green-900" : "border-gray-200 dark:border-gray-800"
                      }`}
                    >
                      <Link
                        href={`/projects/${projectId}/apps/${appName}/builds/${b.id}${envId ? `?envId=${envId}` : ""}`}
                        data-ux="app_deploy_feed:open_build_row"
                        className="min-w-0 flex-1"
                      >
                        <div className="flex flex-wrap items-center gap-2">
                          {isCurrent && <CurrentBadge />}
                          {isPending && <PendingBadge />}
                          <BuildStatusBadge status={b.status} />
                          {resolved.kind === "sha" ? (
                            <>
                              <span className="font-mono text-xs text-gray-500 dark:text-gray-400">{resolved.sha.slice(0, 7)}</span>
                              <span className="text-xs text-gray-400 dark:text-gray-500">{b.branch}</span>
                            </>
                          ) : (
                            <span className="text-xs text-gray-400 dark:text-gray-500">{formatCommitLabel(resolved, t)}</span>
                          )}
                          <span className="inline-flex items-center rounded-full bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-600 dark:bg-slate-800 dark:text-slate-300">
                            {buildTriggerLabel(b.trigger, b.pr_number ?? null, t)}
                          </span>
                          {b.pr_number != null && (
                            <span className="inline-flex items-center rounded-full bg-purple-100 px-2 py-0.5 text-xs font-medium text-purple-700 dark:bg-purple-950/40 dark:text-purple-300">
                              {t("previews.pr", { n: b.pr_number })}
                            </span>
                          )}
                        </div>
                        {b.commit_message && <p className="mt-1 truncate text-sm text-gray-700 dark:text-gray-200">{b.commit_message}</p>}
                        {image && <p className="mt-1 truncate font-mono text-xs text-gray-400 dark:text-gray-500">{image}</p>}
                        <p className="mt-0.5 text-xs text-gray-400 dark:text-gray-500">{timeAgo(b.created_at)}</p>
                      </Link>
                      <div className="flex shrink-0 items-center gap-3 pl-4">
                        {canDeploy && isBuildActive(b.status) && (
                          <button
                            onClick={() => handleCancel(b.id)}
                            data-ux="app_deploy_feed:cancel_build"
                            disabled={actionId === b.id}
                            className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-xs font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50"
                          >
                            {actionId === b.id ? t("apps.deployments.cancelingBuild") : t("apps.deployments.cancelBuild")}
                          </button>
                        )}
                        {canDeploy && b.status === "failed" && isRepoFixable(b.fail_reason) && (
                          <button
                            onClick={() => handleAutofix(b)}
                            data-ux="app_deploy_feed:autofix"
                            disabled={actionId === b.id}
                            className="rounded-lg border border-blue-300 dark:border-blue-800 px-3 py-1.5 text-xs font-medium text-blue-700 dark:text-blue-300 hover:bg-blue-50 dark:hover:bg-blue-950/40 disabled:opacity-50"
                          >
                            {actionId === b.id ? t("apps.deployments.autofixing") : t("apps.deployments.autofix")}
                          </button>
                        )}
                        {dep && <DeployActions dep={dep} isNewest={isNewest} />}
                        <Link
                          href={`/projects/${projectId}/apps/${appName}/builds/${b.id}${envId ? `?envId=${envId}` : ""}`}
                          data-ux="app_deploy_feed:build_logs"
                          className="text-sm font-medium text-blue-600 dark:text-blue-400 hover:text-blue-700"
                        >
                          {t("apps.deployments.logs")}
                        </Link>
                      </div>
                    </div>
                  );
                }

                const dep = row.dep;
                const depResolved = resolveCommit(dep);
                const isNewest = isNewestDeployment(dep.id, deployments);
                const isPending = isPendingDeployment(dep.id, deployments);
                return (
                  <div
                    key={dep.id}
                    className={`flex items-center justify-between rounded-xl border bg-white dark:bg-gray-900 px-5 py-4 shadow-sm ${
                      dep.is_current ? "border-green-300 ring-1 ring-green-200 dark:ring-green-900" : "border-gray-200 dark:border-gray-800"
                    }`}
                  >
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        {dep.is_current && <CurrentBadge />}
                        {isPending && <PendingBadge />}
                        <span className="inline-flex items-center rounded-full bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-600 dark:bg-slate-800 dark:text-slate-300">
                          {buildTriggerLabel(dep.trigger, null, t)}
                        </span>
                        {depResolved.kind === "sha" && (
                          <span className="font-mono text-xs text-gray-500 dark:text-gray-400">{depResolved.sha.slice(0, 7)}</span>
                        )}
                        {depResolved.kind === "sha" && dep.branch && <span className="text-xs text-gray-400 dark:text-gray-500">{dep.branch}</span>}
                        {depResolved.kind === "branch" && (
                          <span className="text-xs text-gray-400 dark:text-gray-500">{t("common.commit.branchLatest", { branch: depResolved.branch })}</span>
                        )}
                        {depResolved.kind === "archive" && (
                          <span className="text-xs text-gray-400 dark:text-gray-500">{formatCommitLabel(depResolved, t)}</span>
                        )}
                        {depResolved.kind === "none" && <span className="text-xs text-gray-400 dark:text-gray-500">{t("common.commit.unknown")}</span>}
                      </div>
                      <p className="mt-1 truncate font-mono text-xs text-gray-400 dark:text-gray-500">{dep.image_uri}</p>
                      <p className="mt-0.5 text-xs text-gray-400 dark:text-gray-500">{timeAgo(dep.created_at)}</p>
                    </div>
                    <div className="flex shrink-0 items-center gap-2 pl-4">
                      <DeployActions dep={dep} isNewest={isNewest} />
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </section>
      )}
    </div>
  );
}

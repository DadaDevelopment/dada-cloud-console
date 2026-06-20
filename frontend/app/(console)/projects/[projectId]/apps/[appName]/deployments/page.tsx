"use client";
import { useCallback, useEffect, useState } from "react";
import { useParams, useSearchParams, useRouter } from "next/navigation";
import Link from "next/link";
import { buildsApi, deploymentsApi } from "@/lib/api";
import type { Build, Deployment } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { timeAgo } from "@/lib/format";
import { BuildStatusBadge, isBuildActive } from "@/components/deploy/build-status-badge";

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
        const msg = err instanceof Error ? err.message : "Failed to load deployments";
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

  // Poll every 3s while any build is in-progress (mirrors operations/page.tsx).
  useEffect(() => {
    const hasActive = builds.some((b) => isBuildActive(b.status));
    if (!hasActive) return;
    const interval = setInterval(() => void load(true), 3000);
    return () => clearInterval(interval);
  }, [builds, load]);

  async function handleTrigger() {
    setTriggering(true);
    setNotice(null);
    try {
      const { build } = await buildsApi.trigger(projectId, envId, appName);
      setNotice(`Build queued · ${build.commit_sha?.slice(0, 7) ?? build.id.slice(0, 7)}`);
      await load(true);
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Failed to trigger build";
      setError(/409|not connected/i.test(msg) ? "No repository is connected to this app yet." : msg);
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
      setError(err instanceof Error ? err.message : "Failed to cancel build");
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
      setError(err instanceof Error ? err.message : `Failed to ${kind}`);
      setActionId(null);
    }
  }

  return (
    <div>
      <div className="mb-8 flex items-start justify-between">
        <div>
          <Breadcrumb
            items={[
              { label: "Projects", href: "/projects" },
              { label: "Overview", href: `/projects/${projectId}` },
              { label: "Applications", href: `/projects/${projectId}/apps` },
              { label: appName, href: `/projects/${projectId}/apps/${appName}${envId ? `?envId=${envId}` : ""}` },
              { label: "Deployments" },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900">
            <span className="font-mono">{appName}</span>
            <span className="ml-2 text-lg font-normal text-gray-400">/ deployments</span>
          </h1>
        </div>
        {canDeploy && !unavailable && (
          <button
            onClick={handleTrigger}
            disabled={triggering}
            className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50 transition-colors"
          >
            {triggering ? <><Spinner size="sm" /> Queuing…</> : "Trigger build"}
          </button>
        )}
      </div>

      {notice && (
        <div className="mb-4 rounded-lg border border-green-200 bg-green-50 px-4 py-3 text-sm text-green-700">{notice}</div>
      )}
      {error && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{error}</div>
      )}

      {loading ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : unavailable ? (
        <div className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
          The build &amp; deploy subsystem is not configured in this environment yet. Connect a repository and deploy the
          build-agent to enable source builds.
          <Link href={`/projects/${projectId}/git${envId ? `?envId=${envId}` : ""}`} className="ml-1 font-medium underline">
            Manage Git &amp; Builds
          </Link>
        </div>
      ) : (
        <div className="space-y-10">
          {/* Deployments */}
          <section>
            <h2 className="mb-3 text-lg font-semibold text-gray-900">Deployments</h2>
            {deployments.length === 0 ? (
              <p className="rounded-xl border border-dashed border-gray-300 bg-gray-50 px-5 py-8 text-center text-sm text-gray-500">
                No deployments yet.
              </p>
            ) : (
              <div className="space-y-3">
                {deployments.map((dep) => (
                  <div
                    key={dep.id}
                    className={`flex items-center justify-between rounded-xl border bg-white px-5 py-4 shadow-sm ${
                      dep.is_current ? "border-green-300 ring-1 ring-green-200" : "border-gray-200"
                    }`}
                  >
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        {dep.is_current && (
                          <span className="inline-flex items-center rounded-full bg-green-100 px-2 py-0.5 text-xs font-semibold text-green-700">
                            Current
                          </span>
                        )}
                        <span className="inline-flex items-center rounded-full bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-600">
                          {dep.trigger}
                        </span>
                        {dep.commit_sha && <span className="font-mono text-xs text-gray-500">{dep.commit_sha.slice(0, 7)}</span>}
                        {dep.branch && <span className="text-xs text-gray-400">{dep.branch}</span>}
                      </div>
                      <p className="mt-1 truncate font-mono text-xs text-gray-400">{dep.image_uri}</p>
                      <p className="mt-0.5 text-xs text-gray-400">{timeAgo(dep.created_at)}</p>
                    </div>
                    {canDeploy && (
                      <div className="flex shrink-0 items-center gap-2 pl-4">
                        {dep.is_current ? (
                          <button
                            onClick={() => handleDeployAction(dep.id, "promote")}
                            disabled={actionId === dep.id}
                            className="rounded-lg border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
                          >
                            {actionId === dep.id ? "Promoting…" : "Promote to prod"}
                          </button>
                        ) : (
                          <button
                            onClick={() => handleDeployAction(dep.id, "rollback")}
                            disabled={actionId === dep.id}
                            className="rounded-lg border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
                          >
                            {actionId === dep.id ? "Rolling back…" : "Rollback to this"}
                          </button>
                        )}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            )}
          </section>

          {/* Builds */}
          <section>
            <h2 className="mb-3 text-lg font-semibold text-gray-900">Builds</h2>
            {builds.length === 0 ? (
              <p className="rounded-xl border border-dashed border-gray-300 bg-gray-50 px-5 py-8 text-center text-sm text-gray-500">
                No builds yet. Connect a repository and trigger a build.
              </p>
            ) : (
              <div className="space-y-3">
                {builds.map((b) => (
                  <div key={b.id} className="flex items-center justify-between rounded-xl border border-gray-200 bg-white px-5 py-4 shadow-sm">
                    <Link href={`/projects/${projectId}/apps/${appName}/builds/${b.id}${envId ? `?envId=${envId}` : ""}`} className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <BuildStatusBadge status={b.status} />
                        <span className="font-mono text-xs text-gray-500">{b.commit_sha?.slice(0, 7) ?? "—"}</span>
                        <span className="text-xs text-gray-400">{b.branch}</span>
                        <span className="inline-flex items-center rounded-full bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-600">
                          {b.trigger}
                        </span>
                      </div>
                      {b.commit_message && <p className="mt-1 truncate text-sm text-gray-700">{b.commit_message}</p>}
                      <p className="mt-0.5 text-xs text-gray-400">{timeAgo(b.created_at)}</p>
                    </Link>
                    <div className="flex shrink-0 items-center gap-3 pl-4">
                      {canDeploy && isBuildActive(b.status) && (
                        <button
                          onClick={() => handleCancel(b.id)}
                          disabled={actionId === b.id}
                          className="rounded-lg border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
                        >
                          {actionId === b.id ? "Canceling…" : "Cancel"}
                        </button>
                      )}
                      <Link
                        href={`/projects/${projectId}/apps/${appName}/builds/${b.id}${envId ? `?envId=${envId}` : ""}`}
                        className="text-sm font-medium text-blue-600 hover:text-blue-700"
                      >
                        Logs →
                      </Link>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </section>
        </div>
      )}
    </div>
  );
}

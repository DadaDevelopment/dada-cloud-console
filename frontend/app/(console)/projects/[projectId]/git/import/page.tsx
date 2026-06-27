"use client";
import { useCallback, useEffect, useState, FormEvent } from "react";
import { useParams, useSearchParams, useRouter } from "next/navigation";
import Link from "next/link";
import { gitApi } from "@/lib/api";
import type { GitInstallation, GitRemoteRepo, FrameworkDetection } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { useT } from "@/lib/i18n/console/context";

type Step = 1 | 2 | 3;

function toKubeName(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "")
    .slice(0, 63);
}

export default function GitImportPage() {
  const { t } = useT();
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const searchParams = useSearchParams();
  const router = useRouter();

  const { project, selectedEnv, role, loading: isLoadingEnvs } = useProjectContext();
  const envId = searchParams.get("envId") || selectedEnv?.id || "";

  const [step, setStep] = useState<Step>(1);

  const [installations, setInstallations] = useState<GitInstallation[]>([]);
  const [loadingInstalls, setLoadingInstalls] = useState(true);
  const [installError, setInstallError] = useState<string | null>(null);
  const [selectedInstall, setSelectedInstall] = useState<GitInstallation | null>(null);

  const [remoteRepos, setRemoteRepos] = useState<GitRemoteRepo[]>([]);
  const [loadingRepos, setLoadingRepos] = useState(false);
  const [repoError, setRepoError] = useState<string | null>(null);
  const [reposUnavailable, setReposUnavailable] = useState(false);
  const [selectedRepo, setSelectedRepo] = useState<GitRemoteRepo | null>(null);

  const [appName, setAppName] = useState("");
  const [port, setPort] = useState(8080);
  const [profile, setProfile] = useState("small");
  const [branch, setBranch] = useState("");
  const [rootDir, setRootDir] = useState(".");
  const [autoDeploy, setAutoDeploy] = useState(true);
  const [detection, setDetection] = useState<FrameworkDetection | null>(null);
  const [frameworkOverride, setFrameworkOverride] = useState("");
  const [detecting, setDetecting] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  const allowed = canMutate(role);

  useEffect(() => {
    /* eslint-disable react-hooks/set-state-in-effect */
    if (!envId) {
      if (!isLoadingEnvs) setLoadingInstalls(false);
      return;
    }
    setLoadingInstalls(true);
    /* eslint-enable react-hooks/set-state-in-effect */
    gitApi
      .installations(projectId)
      .then((d) => setInstallations(d.installations ?? []))
      .catch((err) => setInstallError(err instanceof Error ? err.message : t("git.import.error.loadInstalls")))
      .finally(() => setLoadingInstalls(false));
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, envId, isLoadingEnvs]);

  async function handleConnectProvider(provider: "github" | "gitlab") {
    setInstallError(null);
    try {
      if (provider === "github") {
        const { installations: avail } = await gitApi.availableInstallations(projectId);
        const list = avail ?? [];
        const toBind = list.filter((a) => !a.bound);
        if (list.length) {
          await Promise.all(toBind.map((a) => gitApi.bindInstallation(projectId, a.installation_id)));
          const d = await gitApi.installations(projectId);
          setInstallations(d.installations ?? []);
          return;
        }
      }
      const { url } = await gitApi.installUrl(projectId, provider);
      window.location.href = url;
    } catch (err) {
      const msg = err instanceof Error ? err.message : t("git.import.error.startInstall");
      setInstallError(
        /503|unavailable|not configured/i.test(msg)
          ? t("git.import.unavailable")
          : msg
      );
    }
  }

  const loadRepos = useCallback(
    async (install: GitInstallation) => {
      setLoadingRepos(true);
      setRepoError(null);
      setReposUnavailable(false);
      try {
        const d = await gitApi.remoteRepos(projectId, install.id);
        setRemoteRepos(d.repos ?? []);
      } catch (err) {
        const msg = err instanceof Error ? err.message : t("git.import.error.loadRepos");
        if (/503|unavailable|not configured/i.test(msg)) setReposUnavailable(true);
        else setRepoError(msg);
      } finally {
        setLoadingRepos(false);
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [projectId]
  );

  function pickInstall(install: GitInstallation) {
    setSelectedInstall(install);
    setStep(2);
    void loadRepos(install);
  }

  const runDetect = useCallback(
    async (repo: GitRemoteRepo, root: string) => {
      if (!selectedInstall) return;
      setDetecting(true);
      setDetection(null);
      try {
        const d = await gitApi.detect(projectId, selectedInstall.id, repo.full_name, root || ".");
        setDetection(d);
      } catch {
        setDetection({ framework: null, build_command: null, install_command: null, output_dir: null });
      } finally {
        setDetecting(false);
      }
    },
    [projectId, selectedInstall]
  );

  function pickRepo(repo: GitRemoteRepo) {
    setSelectedRepo(repo);
    setBranch(repo.default_branch || "main");
    setRootDir(".");
    setAppName(toKubeName(repo.full_name.split("/").pop() || ""));
    setStep(3);
    void runDetect(repo, ".");
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!selectedInstall || !selectedRepo) return;
    setSubmitError(null);
    setSubmitting(true);
    try {
      await gitApi.linkRepo(projectId, envId, {
        installation_id: selectedInstall.id,
        repo_full_name: selectedRepo.full_name,
        app_name: appName,
        production_branch: branch,
        root_dir: rootDir || ".",
        framework_override: frameworkOverride || undefined,
        auto_deploy: autoDeploy,
        port,
        profile,
      });
      router.push(`/projects/${projectId}/git${envId ? `?envId=${envId}` : ""}`);
    } catch (err) {
      const msg = err instanceof Error ? err.message : t("git.import.error.connect");
      setSubmitError(/409|already/i.test(msg) ? t("git.import.alreadyConnected") : msg);
    } finally {
      setSubmitting(false);
    }
  }

  if (isLoadingEnvs) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner size="lg" />
      </div>
    );
  }

  if (!allowed) {
    return (
      <div>
        <Breadcrumb
          items={[
            { label: t("common.crumb.projects"), href: "/projects" },
            { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
            { label: t("nav.git"), href: `/projects/${projectId}/git` },
            { label: t("git.import.title") },
          ]}
        />
        <div className="mt-4 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
          {t("git.import.noPermission")}
        </div>
      </div>
    );
  }

  return (
    <div className="max-w-2xl">
      <Breadcrumb
        items={[
          { label: t("common.crumb.projects"), href: "/projects" },
          { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
          { label: t("nav.git"), href: `/projects/${projectId}/git${envId ? `?envId=${envId}` : ""}` },
          { label: t("git.import.title") },
        ]}
      />
      <h1 className="mt-2 text-2xl font-bold text-gray-900">{t("git.import.title")}</h1>

      <ol className="mb-8 mt-4 flex items-center gap-2 text-sm">
        {([t("git.import.step.account"), t("git.import.step.repository"), t("git.import.step.configure")] as const).map((lbl, i) => {
          const n = (i + 1) as Step;
          const active = step === n;
          const done = step > n;
          return (
            <li key={lbl} className="flex items-center gap-2">
              <span
                className={`flex h-6 w-6 items-center justify-center rounded-full text-xs font-semibold ${
                  active ? "bg-blue-600 text-white" : done ? "bg-green-500 text-white" : "bg-gray-200 text-gray-500"
                }`}
              >
                {done ? "✓" : n}
              </span>
              <span className={active ? "font-medium text-gray-900" : "text-gray-400"}>{lbl}</span>
              {n < 3 && <span className="mx-1 text-gray-300">→</span>}
            </li>
          );
        })}
      </ol>

      {step === 1 && (
        <div>
          {installError && (
            <div className="mb-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{installError}</div>
          )}
          {loadingInstalls ? (
            <div className="flex h-32 items-center justify-center">
              <Spinner />
            </div>
          ) : installations.length === 0 ? (
            <div className="rounded-xl border border-dashed border-gray-300 bg-gray-50 p-8 text-center">
              <p className="text-sm font-medium text-gray-600">{t("git.import.noAccounts.title")}</p>
              <p className="mt-1 text-xs text-gray-400">{t("git.import.noAccounts.hint")}</p>
              <div className="mt-4 flex justify-center gap-3">
                <button onClick={() => handleConnectProvider("github")} className="rounded-lg border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50">
                  {t("git.import.connectGitHub")}
                </button>
                <button onClick={() => handleConnectProvider("gitlab")} className="rounded-lg border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50">
                  {t("git.import.connectGitLab")}
                </button>
              </div>
            </div>
          ) : (
            <div className="space-y-3">
              {installations.map((inst) => (
                <button
                  key={inst.id}
                  onClick={() => pickInstall(inst)}
                  className="flex w-full items-center justify-between rounded-xl border border-gray-200 bg-white px-5 py-4 text-left shadow-sm transition-all hover:border-blue-300 hover:shadow-md"
                >
                  <div className="flex items-center gap-3">
                    {inst.account_avatar_url && (
                      // eslint-disable-next-line @next/next/no-img-element
                      <img src={inst.account_avatar_url} alt="" className="h-8 w-8 rounded-full" />
                    )}
                    <div>
                      <p className="text-sm font-semibold text-gray-900">{inst.account_login}</p>
                      <p className="text-xs text-gray-400">{inst.provider}</p>
                    </div>
                  </div>
                  <span className="text-sm text-blue-600">{t("git.import.select")}</span>
                </button>
              ))}
              <div className="flex gap-3 pt-2 text-xs">
                <button onClick={() => handleConnectProvider("github")} className="text-blue-600 hover:text-blue-700">
                  {t("git.import.connectAnotherGitHub")}
                </button>
                <button onClick={() => handleConnectProvider("gitlab")} className="text-blue-600 hover:text-blue-700">
                  {t("git.import.connectAnotherGitLab")}
                </button>
              </div>
            </div>
          )}
        </div>
      )}

      {step === 2 && selectedInstall && (
        <div>
          <button onClick={() => setStep(1)} className="mb-3 text-xs text-gray-400 hover:text-gray-600">
            {t("git.import.backToAccounts")}
          </button>

          <div className="mb-4">
            <label className="block text-xs font-medium text-gray-500">{t("git.import.accountOrg.label")}</label>
            <select
              value={selectedInstall.id}
              onChange={(e) => {
                const next = installations.find((i) => i.id === e.target.value);
                if (!next) return;
                setSelectedRepo(null);
                setSelectedInstall(next);
                void loadRepos(next);
              }}
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              {installations.map((inst) => (
                <option key={inst.id} value={inst.id}>
                  {inst.account_login} ({inst.provider})
                </option>
              ))}
            </select>
          </div>
          {repoError && (
            <div className="mb-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{repoError}</div>
          )}
          {reposUnavailable ? (
            <div className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
              {t("git.import.reposUnavailable")}
            </div>
          ) : loadingRepos ? (
            <div className="flex h-32 items-center justify-center">
              <Spinner />
            </div>
          ) : remoteRepos.length === 0 ? (
            <p className="rounded-xl border border-dashed border-gray-300 bg-gray-50 p-8 text-center text-sm text-gray-500">
              {t("git.import.noRepos")}
            </p>
          ) : (
            <div className="max-h-[480px] space-y-2 overflow-y-auto">
              {remoteRepos.map((repo) => (
                <button
                  key={repo.full_name}
                  onClick={() => pickRepo(repo)}
                  className="flex w-full items-center justify-between rounded-lg border border-gray-200 bg-white px-4 py-3 text-left shadow-sm transition-all hover:border-blue-300"
                >
                  <div className="min-w-0">
                    <p className="truncate font-mono text-sm font-medium text-gray-900">{repo.full_name}</p>
                    {repo.description && <p className="truncate text-xs text-gray-400">{repo.description}</p>}
                  </div>
                  <div className="flex shrink-0 items-center gap-2 pl-3">
                    {repo.private && <span className="text-xs text-gray-400">{t("git.import.private")}</span>}
                    <span className="text-sm text-blue-600">{t("git.import.importArrow")}</span>
                  </div>
                </button>
              ))}
            </div>
          )}
        </div>
      )}

      {step === 3 && selectedRepo && (
        <form onSubmit={handleSubmit} className="space-y-5">
          <button type="button" onClick={() => setStep(2)} className="text-xs text-gray-400 hover:text-gray-600">
            {t("git.import.backToRepos")}
          </button>

          <div className="rounded-lg border border-gray-200 bg-gray-50 px-4 py-3">
            <p className="font-mono text-sm font-medium text-gray-900">{selectedRepo.full_name}</p>
          </div>

          <div className="rounded-lg border border-gray-200 bg-white px-4 py-3">
            <p className="text-xs font-semibold uppercase tracking-wide text-gray-400">{t("git.import.detectedFramework")}</p>
            {detecting ? (
              <div className="mt-2 flex items-center gap-2 text-sm text-gray-500">
                <Spinner size="sm" /> {t("git.import.detecting")}
              </div>
            ) : detection ? (
              <div className="mt-2 space-y-1 text-sm">
                <p className="font-medium text-gray-900">{detection.framework ?? t("git.import.unknownFramework")}</p>
                {detection.build_command && (
                  <p className="text-xs text-gray-500">build: <span className="font-mono">{detection.build_command}</span></p>
                )}
                {detection.install_command && (
                  <p className="text-xs text-gray-500">install: <span className="font-mono">{detection.install_command}</span></p>
                )}
                {detection.output_dir && (
                  <p className="text-xs text-gray-500">output: <span className="font-mono">{detection.output_dir}</span></p>
                )}
              </div>
            ) : null}
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700">
              {t("git.import.appName.label")} <span className="font-normal text-gray-400">{t("git.import.appName.hint")}</span>
            </label>
            <input
              type="text"
              required
              value={appName}
              onChange={(e) => setAppName(toKubeName(e.target.value))}
              placeholder={t("git.import.appName.placeholder")}
              pattern="[a-z0-9-]+"
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <p className="mt-1 text-xs text-gray-400">
              {t("git.import.appName.help")}
            </p>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm font-medium text-gray-700">{t("git.import.port.label")}</label>
              <input
                type="number"
                required
                min={1}
                max={65535}
                value={port}
                onChange={(e) => setPort(parseInt(e.target.value, 10) || 8080)}
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700">{t("git.import.profile.label")}</label>
              <select
                value={profile}
                onChange={(e) => setProfile(e.target.value)}
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              >
                <option value="small">small</option>
                <option value="medium">medium</option>
                <option value="large">large</option>
              </select>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm font-medium text-gray-700">{t("git.import.branch.label")}</label>
              <input
                type="text"
                required
                value={branch}
                onChange={(e) => setBranch(e.target.value)}
                placeholder="main"
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700">{t("git.import.rootDir.label")}</label>
              <input
                type="text"
                value={rootDir}
                onChange={(e) => setRootDir(e.target.value)}
                onBlur={() => selectedRepo && runDetect(selectedRepo, rootDir)}
                placeholder="."
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700">
              {t("git.import.frameworkOverride.label")} <span className="font-normal text-gray-400">{t("common.optional")}</span>
            </label>
            <input
              type="text"
              value={frameworkOverride}
              onChange={(e) => setFrameworkOverride(e.target.value)}
              placeholder={detection?.framework ?? "auto"}
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div className="flex items-center justify-between rounded-lg border border-gray-200 px-4 py-3">
            <div>
              <p className="text-sm font-medium text-gray-700">{t("git.import.autoDeploy.label")}</p>
              <p className="text-xs text-gray-400">{t("git.import.autoDeploy.hint")}</p>
            </div>
            <button
              type="button"
              onClick={() => setAutoDeploy((v) => !v)}
              className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 ${
                autoDeploy ? "bg-blue-600" : "bg-gray-200"
              }`}
              role="switch"
              aria-checked={autoDeploy}
            >
              <span className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${autoDeploy ? "translate-x-6" : "translate-x-1"}`} />
            </button>
          </div>

          {submitError && (
            <div role="alert" className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
              {submitError}
            </div>
          )}

          <div className="flex justify-end gap-3 pt-1">
            <Link
              href={`/projects/${projectId}/git${envId ? `?envId=${envId}` : ""}`}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100 transition-colors"
            >
              {t("common.cancel")}
            </Link>
            <button
              type="submit"
              disabled={submitting || !appName}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {submitting ? (
                <>
                  <Spinner size="sm" /> {t("git.import.connecting")}
                </>
              ) : (
                t("git.import.connect")
              )}
            </button>
          </div>
        </form>
      )}
    </div>
  );
}

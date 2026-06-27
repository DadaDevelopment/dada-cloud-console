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
import { timeAgo } from "@/lib/format";
import { Search, Lock, ChevronDown } from "lucide-react";

type Step = 1 | 2 | 3;

type FrameworkPreset = { id: string; label: string; port: number };
type PresetGroup = { group: string; items: FrameworkPreset[] };

const FRAMEWORK_PRESETS: PresetGroup[] = [
  {
    group: "Java",
    items: [
      { id: "spring-maven", label: "Spring Boot (Maven)", port: 8080 },
      { id: "spring-gradle", label: "Spring Boot (Gradle)", port: 8080 },
    ],
  },
  {
    group: "Python",
    items: [
      { id: "fastapi", label: "FastAPI", port: 8000 },
      { id: "flask", label: "Flask", port: 5000 },
      { id: "django", label: "Django", port: 8000 },
    ],
  },
  {
    group: "JavaScript / TypeScript",
    items: [
      { id: "nextjs", label: "Next.js", port: 3000 },
      { id: "nestjs", label: "NestJS", port: 3000 },
      { id: "node", label: "Node.js / Express", port: 3000 },
      { id: "vite", label: "Vite", port: 4173 },
      { id: "remix", label: "Remix", port: 3000 },
    ],
  },
  {
    group: "Static",
    items: [{ id: "static", label: "Static site", port: 80 }],
  },
];

const PRESET_BY_ID = new Map(FRAMEWORK_PRESETS.flatMap((g) => g.items).map((p) => [p.id, p]));

function GithubMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden className={className}>
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0016 8c0-4.42-3.58-8-8-8z" />
    </svg>
  );
}

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
  const [repoQuery, setRepoQuery] = useState("");

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

          <label className="block text-xs font-medium text-gray-500">{t("git.import.accountOrg.label")}</label>
          <div className="mb-4 mt-1 flex flex-col gap-2 sm:flex-row">
            <div className="relative sm:w-64">
              <GithubMark className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-500" />
              <select
                value={selectedInstall.id}
                onChange={(e) => {
                  const next = installations.find((i) => i.id === e.target.value);
                  if (!next) return;
                  setSelectedRepo(null);
                  setRepoQuery("");
                  setSelectedInstall(next);
                  void loadRepos(next);
                }}
                className="w-full appearance-none rounded-lg border border-gray-300 bg-white py-2 pl-9 pr-9 text-sm font-medium text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              >
                {installations.map((inst) => (
                  <option key={inst.id} value={inst.id}>
                    {inst.account_login}
                  </option>
                ))}
              </select>
              <ChevronDown className="pointer-events-none absolute right-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
            </div>
            <div className="relative flex-1">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
              <input
                type="text"
                value={repoQuery}
                onChange={(e) => setRepoQuery(e.target.value)}
                placeholder={t("git.import.searchPlaceholder")}
                className="w-full rounded-lg border border-gray-300 bg-white py-2 pl-9 pr-3 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
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
            (() => {
              const q = repoQuery.trim().toLowerCase();
              const filtered = q
                ? remoteRepos.filter((r) => r.full_name.toLowerCase().includes(q))
                : remoteRepos;
              if (filtered.length === 0) {
                return (
                  <p className="rounded-xl border border-dashed border-gray-300 bg-gray-50 p-8 text-center text-sm text-gray-500">
                    {t("git.import.noMatch")}
                  </p>
                );
              }
              return (
                <div className="max-h-[480px] divide-y divide-gray-100 overflow-y-auto rounded-xl border border-gray-200 bg-white shadow-sm">
                  {filtered.map((repo) => {
                    const shortName = repo.full_name.split("/").pop() || repo.full_name;
                    return (
                      <div
                        key={repo.full_name}
                        className="group flex items-center justify-between gap-3 px-4 py-3 transition-colors hover:bg-gray-50"
                      >
                        <div className="flex min-w-0 items-center gap-3">
                          <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-gray-100 text-gray-500">
                            <GithubMark className="h-4 w-4" />
                          </span>
                          <div className="min-w-0">
                            <div className="flex items-center gap-2">
                              <p className="truncate text-sm font-medium text-gray-900">{shortName}</p>
                              {repo.private && <Lock className="h-3 w-3 shrink-0 text-gray-400" />}
                            </div>
                            <p className="truncate text-xs text-gray-400">
                              {repo.updated_at ? timeAgo(repo.updated_at) : repo.full_name}
                            </p>
                          </div>
                        </div>
                        <button
                          onClick={() => pickRepo(repo)}
                          className="shrink-0 rounded-lg border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 shadow-sm transition-colors hover:border-blue-400 hover:text-blue-600 group-hover:border-blue-300"
                        >
                          {t("git.import.importButton")}
                        </button>
                      </div>
                    );
                  })}
                </div>
              );
            })()
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

          <div>
            <label className="block text-sm font-medium text-gray-700">{t("git.import.framework.label")}</label>
            <div className="relative mt-1">
              <select
                value={frameworkOverride}
                onChange={(e) => {
                  const id = e.target.value;
                  setFrameworkOverride(id);
                  const preset = PRESET_BY_ID.get(id);
                  if (preset) setPort(preset.port);
                }}
                className="block w-full appearance-none rounded-lg border border-gray-300 bg-white px-3 py-2 pr-9 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              >
                <option value="">{t("git.import.framework.auto")}</option>
                {FRAMEWORK_PRESETS.map((g) => (
                  <optgroup key={g.group} label={g.group}>
                    {g.items.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.label}
                      </option>
                    ))}
                  </optgroup>
                ))}
              </select>
              <ChevronDown className="pointer-events-none absolute right-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
            </div>
            <p className="mt-1 text-xs text-gray-400">{t("git.import.framework.hint")}</p>
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

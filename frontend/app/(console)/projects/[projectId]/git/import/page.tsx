"use client";
import { useCallback, useEffect, useRef, useState, FormEvent } from "react";
import { useParams, useSearchParams } from "next/navigation";
import Link from "next/link";
import { gitApi, buildsApi, isGithubAccessRequiredError, classifyConnectRepoConflict } from "@/lib/api";
import type { GitInstallation, GitRemoteRepo, FrameworkDetection, Build, AvailableInstallation } from "@/lib/types";
import { BuildLogViewer } from "@/components/deploy/build-log-viewer";
import { FrameworkLogo } from "@/components/deploy/framework-logo";
import { Select, SelectContent, SelectItem, SelectLabel, SelectTrigger, SelectValue, SelectGroup } from "@/components/ui/select";
import { BuildStatusBadge, isBuildActive } from "@/components/deploy/build-status-badge";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { useT } from "@/lib/i18n/console/context";
import { TemplateDeployCards } from "@/components/console/template-deploy-cards";
import { UploadDeployCard } from "@/components/deploy/upload-deploy";
import { ConnectByUrlDialog } from "@/components/deploy/connect-by-url-dialog";
import { timeAgo } from "@/lib/format";
import { CopyButton } from "@/components/ui/copy-button";
import { githubActionsStep, deployCurl } from "@/lib/deploy-snippet";
import { trackBuildStart } from "@/lib/build-watch";
import { connectToGithub } from "@/lib/git-install-connect";
import { Search, Lock, Plus } from "lucide-react";

type FrameworkPreset = { id: string; label: string; port: number };
type PresetGroup = { group: string; items: FrameworkPreset[] };
type GitRemoteRepoCandidate = GitRemoteRepo & {
  installationId: string;
  accountLogin: string;
  accountType: string;
};

type WizardDraft = {
  selectedRepo: GitRemoteRepoCandidate | null;
  appName: string;
  port: number;
  worker: boolean;
  profile: string;
  branch: string;
  rootDir: string;
  autoDeploy: boolean;
  frameworkOverride: string;
  frameworkTouched: boolean;
  portTouched: boolean;
  detection: FrameworkDetection | null;
  buildId: string | null;
};

function draftKey(projectId: string) {
  return `dada.import-draft.${projectId}`;
}

function pendingRepoKey(projectId: string) {
  return `dada.import-pending-repo.${projectId}`;
}

function saveDraft(projectId: string, d: WizardDraft) {
  try {
    sessionStorage.setItem(draftKey(projectId), JSON.stringify(d));
  } catch {
    return;
  }
}

function loadDraft(projectId: string): WizardDraft | null {
  try {
    const raw = sessionStorage.getItem(draftKey(projectId));
    return raw ? (JSON.parse(raw) as WizardDraft) : null;
  } catch {
    return null;
  }
}

function clearDraft(projectId: string) {
  try {
    sessionStorage.removeItem(draftKey(projectId));
  } catch {
    return;
  }
}

const FRAMEWORK_PRESETS: PresetGroup[] = [
  {
    group: "Java / JVM",
    items: [
      { id: "spring-maven", label: "Spring Boot (Maven)", port: 8080 },
      { id: "spring-gradle", label: "Spring Boot (Gradle)", port: 8080 },
      { id: "scala", label: "Scala / JVM", port: 8080 },
      { id: "maven", label: "Maven", port: 8080 },
      { id: "gradle", label: "Gradle", port: 8080 },
      { id: "go", label: "Go", port: 8080 },
    ],
  },
  {
    group: "Python",
    items: [
      { id: "fastapi", label: "FastAPI", port: 8000 },
      { id: "flask", label: "Flask", port: 5000 },
      { id: "django", label: "Django", port: 8000 },
      { id: "python", label: "Python", port: 8000 },
    ],
  },
  {
    group: "JavaScript / TypeScript",
    items: [
      { id: "nextjs", label: "Next.js", port: 3000 },
      { id: "nuxt", label: "Nuxt", port: 3000 },
      { id: "sveltekit", label: "SvelteKit", port: 3000 },
      { id: "react", label: "React", port: 3000 },
      { id: "nestjs", label: "NestJS", port: 3000 },
      { id: "express", label: "Express", port: 3000 },
      { id: "fastify", label: "Fastify", port: 3000 },
      { id: "node", label: "Node.js", port: 3000 },
      { id: "vite", label: "Vite", port: 4173 },
      { id: "remix", label: "Remix", port: 3000 },
    ],
  },
  {
    group: "Static",
    items: [
      { id: "static", label: "Static site", port: 80 },
      { id: "dockerfile", label: "Dockerfile", port: 8080 },
    ],
  },
];

const PRESET_BY_ID = new Map(FRAMEWORK_PRESETS.flatMap((g) => g.items).map((p) => [p.id, p]));

const DETECTED_FRAMEWORK_TO_PRESET_ID: Record<string, string> = {
  spring: "spring-maven",
  "spring-boot": "spring-maven",
  "spring-maven": "spring-maven",
  "spring-gradle": "spring-gradle",
  scala: "scala",
  maven: "maven",
  gradle: "gradle",
  go: "go",
  python: "python",
  fastapi: "fastapi",
  django: "django",
  flask: "flask",
  nextjs: "nextjs",
  nuxt: "nuxt",
  sveltekit: "sveltekit",
  react: "react",
  express: "express",
  fastify: "fastify",
  remix: "remix",
  vite: "vite",
  node: "node",
  static: "static",
  dockerfile: "dockerfile",
};

function detectedPresetId(framework: string | null): string | null {
  if (!framework) return null;
  const normalized = framework.toLowerCase();
  return DETECTED_FRAMEWORK_TO_PRESET_ID[normalized] ?? (PRESET_BY_ID.has(normalized) ? normalized : null);
}

function detectedPort(detection: FrameworkDetection | null): number | null {
  if (!detection) return null;
  if (typeof detection.port === "number" && Number.isFinite(detection.port) && detection.port > 0) {
    return detection.port;
  }
  const presetId = detectedPresetId(detection.framework);
  if (!presetId) return null;
  return PRESET_BY_ID.get(presetId)?.port ?? null;
}

function frameworkLabel(framework: string | null): string {
  if (!framework) return "";
  return PRESET_BY_ID.get(framework)?.label ?? framework;
}

function GithubMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden className={className}>
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0016 8c0-4.42-3.58-8-8-8z" />
    </svg>
  );
}

function SectionHeader({ n, label, done, active }: { n: number; label: string; done: boolean; active: boolean }) {
  return (
    <div className="flex items-center gap-3">
      <span
        className={`flex h-6 w-6 items-center justify-center rounded-full text-xs font-semibold ${
          done ? "bg-green-500 text-white" : active ? "bg-blue-600 text-white" : "bg-gray-200 dark:bg-gray-700 text-gray-400 dark:text-gray-500"
        }`}
      >
        {done ? "✓" : n}
      </span>
      <h2 className={`text-sm font-semibold ${active || done ? "text-gray-900 dark:text-gray-100" : "text-gray-400 dark:text-gray-500"}`}>{label}</h2>
    </div>
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

  const { project, selectedEnv, role, loading: isLoadingEnvs } = useProjectContext();
  const envId = searchParams.get("envId") || selectedEnv?.id || "";

  const [installations, setInstallations] = useState<GitInstallation[]>([]);
  const [loadingInstalls, setLoadingInstalls] = useState(true);
  const [installError, setInstallError] = useState<string | null>(null);
  const [connectingProvider, setConnectingProvider] = useState<"github" | null>(null);

  const [remoteRepos, setRemoteRepos] = useState<GitRemoteRepoCandidate[]>([]);
  const [loadingRepos, setLoadingRepos] = useState(false);
  const [repoError, setRepoError] = useState<string | null>(null);
  const [reposUnavailable, setReposUnavailable] = useState(false);
  const [selectedRepo, setSelectedRepo] = useState<GitRemoteRepoCandidate | null>(null);
  const [repoQuery, setRepoQuery] = useState("");
  // When a repo is picked the list collapses into a compact bar; "Change" reopens it.
  const [repoPickerOpen, setRepoPickerOpen] = useState(true);

  const [appName, setAppName] = useState("");
  const [port, setPort] = useState(8080);
  const [worker, setWorker] = useState(false);
  const [profile, setProfile] = useState("small");
  const [branch, setBranch] = useState("");
  const [rootDir, setRootDir] = useState(".");
  const [autoDeploy, setAutoDeploy] = useState(true);
  const [detection, setDetection] = useState<FrameworkDetection | null>(null);
  const [frameworkOverride, setFrameworkOverride] = useState("");
  const [frameworkTouched, setFrameworkTouched] = useState(false);
  const [portTouched, setPortTouched] = useState(false);
  const [detecting, setDetecting] = useState(false);
  const [detectError, setDetectError] = useState(false);
  const [ghaGuideOpen, setGhaGuideOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [githubAccessRequired, setGithubAccessRequired] = useState(false);
  const [urlDialogOpen, setUrlDialogOpen] = useState(false);

  // Deploy phase. Linking the repo kicks off the first build; we stay on the same
  // continuous page and stream its logs until a terminal status.
  const [build, setBuild] = useState<Build | null>(null);
  const [deployError, setDeployError] = useState<string | null>(null);
  const [hydrated, setHydrated] = useState(false);

  const allowed = canMutate(role);
  // Once a deploy is in flight (or finished) the earlier sections lock so the
  // spec the build was triggered with can't drift under the user.
  const deploying = submitting || !!build || !!deployError;

  const refreshInstallations = useCallback(
    async (autoBindAvailable: boolean) => {
      setLoadingInstalls(true);
      setInstallError(null);
      try {
        let bound = (await gitApi.installations(projectId)).installations ?? [];

        if (autoBindAvailable) {
          let available: AvailableInstallation[] = [];
          try {
            available = (await gitApi.availableInstallations(projectId)).installations ?? [];
          } catch (err) {
            if (bound.length === 0) throw err;
          }

          const toBind = available.filter((item) => !item.bound);
          if (toBind.length > 0) {
            await Promise.all(toBind.map((item) => gitApi.bindInstallation(projectId, item.installation_id)));
            bound = (await gitApi.installations(projectId)).installations ?? [];
          }
        }

        setInstallations(bound);
      } catch (err) {
        setInstallError(err instanceof Error ? err.message : t("git.import.error.loadInstalls"));
      } finally {
        setLoadingInstalls(false);
      }
    },
    [projectId, t]
  );

  useEffect(() => {
    /* eslint-disable react-hooks/set-state-in-effect */
    if (!envId) {
      if (!isLoadingEnvs) setLoadingInstalls(false);
      return;
    }
    /* eslint-enable react-hooks/set-state-in-effect */
    void refreshInstallations(allowed);
  }, [projectId, envId, isLoadingEnvs, allowed, refreshInstallations]);

  const loadRepos = useCallback(
    async (installs: GitInstallation[]) => {
      setLoadingRepos(true);
      setRepoError(null);
      setReposUnavailable(false);
      try {
        const results = await Promise.allSettled(
          installs.map(async (install) => {
            const d = await gitApi.remoteRepos(projectId, install.id);
            return (d.repos ?? []).map((repo) => ({
              ...repo,
              installationId: install.id,
              accountLogin: install.account_login,
              accountType: install.account_type,
            }));
          })
        );

        const merged = new Map<string, GitRemoteRepoCandidate>();
        let successCount = 0;
        let unavailableCount = 0;
        let firstError: string | null = null;

        for (const result of results) {
          if (result.status === "fulfilled") {
            successCount++;
            for (const repo of result.value) {
              const prev = merged.get(repo.full_name);
              if (!prev) {
                merged.set(repo.full_name, repo);
                continue;
              }
              const prevUpdated = prev.updated_at ? Date.parse(prev.updated_at) : 0;
              const nextUpdated = repo.updated_at ? Date.parse(repo.updated_at) : 0;
              if (nextUpdated > prevUpdated) merged.set(repo.full_name, repo);
            }
            continue;
          }

          const msg = result.reason instanceof Error ? result.reason.message : t("git.import.error.loadRepos");
          if (/503|unavailable|not configured/i.test(msg)) unavailableCount++;
          if (!firstError) firstError = msg;
        }

        const repos = Array.from(merged.values()).sort((a, b) => {
          const aUpdated = a.updated_at ? Date.parse(a.updated_at) : 0;
          const bUpdated = b.updated_at ? Date.parse(b.updated_at) : 0;
          if (aUpdated !== bUpdated) return bUpdated - aUpdated;
          return a.full_name.localeCompare(b.full_name);
        });
        setRemoteRepos(repos);

        if (repos.length === 0 && unavailableCount === installs.length) {
          setReposUnavailable(true);
        } else if (repos.length === 0 && firstError) {
          setRepoError(firstError);
        } else if (successCount === 0 && firstError) {
          setRepoError(firstError);
        }
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

  // Repo picker is fed from the union of all bound GitHub installations so the
  // user sees personal + org repositories as one searchable list.
  useEffect(() => {
    if (installations.length === 0) return;
    const currentInstallations = installations;
    const timer = window.setTimeout(() => {
      void loadRepos(currentInstallations);
    }, 0);
    return () => window.clearTimeout(timer);
  }, [installations, loadRepos]);

  const repoParam = searchParams.get("repo");
  const repoPrefillDone = useRef(false);

  async function handleConnectProvider(provider: "github", forceInstall = false) {
    setInstallError(null);
    setConnectingProvider(provider);
    try {
      if (!forceInstall) {
        const { installations: avail } = await gitApi.availableInstallations(projectId);
        const list = avail ?? [];
        const toBind = list.filter((a) => !a.bound);
        if (list.length) {
          await Promise.all(toBind.map((a) => gitApi.bindInstallation(projectId, a.installation_id)));
          await refreshInstallations(false);
          return;
        }
      }
      await connectToGithub({
        fetchInstallUrl: async () => (await gitApi.installUrl(projectId, provider)).url,
        navigate: (url) => {
          window.location.href = url;
        },
      });
    } catch (err) {
      const msg = err instanceof Error ? err.message : t("git.import.error.startInstall");
      setInstallError(/503|unavailable|not configured/i.test(msg) ? t("git.import.unavailable") : msg);
    } finally {
      setConnectingProvider(null);
    }
  }

  const runDetect = useCallback(
    async (repo: GitRemoteRepoCandidate, root: string) => {
      setDetecting(true);
      setDetectError(false);
      setDetection(null);
      try {
        const d = await gitApi.detect(projectId, repo.installationId, repo.full_name, root || ".");
        const presetId = detectedPresetId(d.framework);
        if (presetId && !frameworkTouched) {
          setFrameworkOverride(presetId);
        }
        if (!frameworkTouched && !portTouched) {
          const nextPort = detectedPort(d);
          if (typeof nextPort === "number" && Number.isFinite(nextPort)) {
            setPort(nextPort);
          }
        }
        setDetection(d);
      } catch {
        setDetection(null);
        setDetectError(true);
      } finally {
        setDetecting(false);
      }
    },
    [frameworkTouched, portTouched, projectId]
  );

  const pickRepo = useCallback(
    (repo: GitRemoteRepoCandidate) => {
      setBuild(null);
      setDeployError(null);
      setSelectedRepo(repo);
      setRepoPickerOpen(false);
      setBranch(repo.default_branch || "main");
      setRootDir(".");
      setAppName(toKubeName(repo.full_name.split("/").pop() || ""));
      setFrameworkOverride("");
      setFrameworkTouched(false);
      setPortTouched(false);
      setPort(8080);
      try {
        sessionStorage.removeItem(pendingRepoKey(projectId));
      } catch {}
      void runDetect(repo, ".");
    },
    [runDetect, projectId]
  );

  const [pendingRepo, setPendingRepo] = useState<string | null>(null);

  useEffect(() => {
    if (repoParam) {
      try {
        sessionStorage.setItem(pendingRepoKey(projectId), repoParam);
      } catch {}
      const timer = window.setTimeout(() => setPendingRepo(repoParam), 0);
      return () => window.clearTimeout(timer);
    }
    const timer = window.setTimeout(() => {
      let stored: string | null = null;
      try {
        stored = sessionStorage.getItem(pendingRepoKey(projectId));
      } catch {}
      if (stored) setPendingRepo(stored);
    }, 0);
    return () => window.clearTimeout(timer);
  }, [repoParam, projectId]);

  useEffect(() => {
    if (repoPrefillDone.current) return;
    if (!pendingRepo || selectedRepo || deploying) return;
    if (remoteRepos.length === 0) return;
    const match = remoteRepos.find((r) => r.full_name.toLowerCase() === pendingRepo.toLowerCase());
    if (!match) return;
    repoPrefillDone.current = true;
    const timer = window.setTimeout(() => pickRepo(match), 0);
    return () => window.clearTimeout(timer);
  }, [pendingRepo, selectedRepo, deploying, remoteRepos, pickRepo]);

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!selectedRepo) return;
    setSubmitError(null);
    setGithubAccessRequired(false);
    setDeployError(null);
    setSubmitting(true);
    try {
      await gitApi.linkRepo(projectId, envId, {
        installation_id: selectedRepo.installationId,
        repo_full_name: selectedRepo.full_name,
        app_name: appName,
        production_branch: branch,
        root_dir: rootDir || ".",
        framework_override: frameworkOverride || undefined,
        auto_deploy: autoDeploy,
        port: worker ? 0 : port,
        worker,
        profile,
      });
    } catch (err) {
      const apiErr = err as { status?: number; code?: string } | undefined;
      if (isGithubAccessRequiredError(apiErr?.status, apiErr?.code)) {
        setSubmitError(t("git.import.error.githubAccessRequired"));
        setGithubAccessRequired(true);
        setSubmitting(false);
        return;
      }
      const conflict = classifyConnectRepoConflict(apiErr?.status, apiErr?.code);
      if (conflict === "app_name_taken") {
        setSubmitError(t("git.import.byUrl.error.appNameTaken"));
        setSubmitting(false);
        return;
      }
      if (conflict === "repo_linked_to_other_project") {
        setSubmitError(t("git.import.byUrl.error.repoLinkedToOtherProject"));
        setSubmitting(false);
        return;
      }
      if (conflict !== "repo_already_connected") {
        setSubmitError(err instanceof Error ? err.message : t("git.import.error.connect"));
        setSubmitting(false);
        return;
      }
    }
    // Repo linked — kick off the first build; the deploy section reveals below.
    try {
      const { build: b } = await buildsApi.trigger(projectId, envId, appName);
      if (b?.id) trackBuildStart({ projectId, envId, appName, buildId: b.id });
      setBuild(b);
    } catch (err) {
      setDeployError(err instanceof Error ? err.message : t("git.import.deploy.triggerFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  const triggerDeploy = useCallback(async () => {
    setDeployError(null);
    setBuild(null);
    try {
      const { build: b } = await buildsApi.trigger(projectId, envId, appName);
      if (b?.id) trackBuildStart({ projectId, envId, appName, buildId: b.id });
      setBuild(b);
    } catch (err) {
      setDeployError(err instanceof Error ? err.message : t("git.import.deploy.triggerFailed"));
    }
  }, [projectId, envId, appName, t]);

  useEffect(() => {
    /* eslint-disable react-hooks/set-state-in-effect */
    const d = loadDraft(projectId);
    if (d?.selectedRepo) {
      setSelectedRepo(d.selectedRepo);
      setRepoPickerOpen(false);
    }
    if (d?.appName) setAppName(d.appName);
    if (typeof d?.port === "number") setPort(d.port);
    if (typeof d?.worker === "boolean") setWorker(d.worker);
    if (d?.profile) setProfile(d.profile);
    if (d?.branch) setBranch(d.branch);
    if (d?.rootDir) setRootDir(d.rootDir);
    if (typeof d?.autoDeploy === "boolean") setAutoDeploy(d.autoDeploy);
    if (d?.frameworkOverride) setFrameworkOverride(d.frameworkOverride);
    if (d?.frameworkTouched) setFrameworkTouched(true);
    if (d?.portTouched) setPortTouched(true);
    if (d?.detection) setDetection(d.detection);
    setHydrated(true);
    /* eslint-enable react-hooks/set-state-in-effect */
    if (d?.buildId) {
      buildsApi
        .get(projectId, d.buildId)
        .then(({ build: b }) => setBuild(b))
        .catch(() => {});
    }
  }, [projectId]);

  useEffect(() => {
    if (!hydrated) return;
    if (!selectedRepo && !build) {
      clearDraft(projectId);
      return;
    }
    saveDraft(projectId, {
      selectedRepo,
      appName,
      port,
      worker,
      profile,
      branch,
      rootDir,
      autoDeploy,
      frameworkOverride,
      frameworkTouched,
      portTouched,
      detection,
      buildId: build?.id ?? null,
    });
  }, [
    hydrated,
    projectId,
    selectedRepo,
    appName,
    port,
    worker,
    profile,
    branch,
    rootDir,
    autoDeploy,
    frameworkOverride,
    frameworkTouched,
    portTouched,
    detection,
    build,
  ]);

  // Poll the build's status while active so the deploy section reaches a terminal
  // state without a manual refresh.
  useEffect(() => {
    if (!build || !isBuildActive(build.status)) return;
    const id = setInterval(() => {
      buildsApi
        .get(projectId, build.id)
        .then(({ build: b }) => setBuild(b))
        .catch(() => {});
    }, 3000);
    return () => clearInterval(id);
  }, [projectId, build]);

  if (isLoadingEnvs || !hydrated) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner size="lg" />
      </div>
    );
  }

  const crumbs = (
    <Breadcrumb
      items={[
        { label: t("common.crumb.projects"), href: "/projects" },
        { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
        { label: t("nav.git"), href: `/projects/${projectId}/git${envId ? `?envId=${envId}` : ""}` },
        { label: t("git.import.title") },
      ]}
    />
  );

  if (!allowed) {
    return (
      <div>
        {crumbs}
        <div className="mt-4 rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-800 dark:text-amber-300">
          {t("git.import.noPermission")}
        </div>
      </div>
    );
  }

  const q = repoQuery.trim().toLowerCase();
  const filteredRepos = q ? remoteRepos.filter((r) => r.full_name.toLowerCase().includes(q)) : remoteRepos;
  const ciWorkflows = detection?.ci_workflows ?? [];

  return (
    <div className="max-w-2xl">
      {crumbs}
      <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("git.import.title")}</h1>
      <p className="mt-1 text-sm text-gray-400 dark:text-gray-500">{t("git.import.subtitle")}</p>

      <div className="mt-8 space-y-8">
        {/* ── 1. Source ── */}
        <section className="space-y-3">
          <SectionHeader n={1} label={t("git.import.section.source")} done={!!selectedRepo} active={!selectedRepo} />

          {!selectedRepo && (
            <Link
              href={`/projects/${projectId}/apps?deploy=image${envId ? `&envId=${envId}` : ""}`}
              className="block text-sm font-medium text-blue-600 dark:text-blue-400 hover:underline"
            >
              {t("deployHooks.wizard.cta")}
            </Link>
          )}

          {installError && (
            <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">{installError}</div>
          )}

          {loadingInstalls ? (
            <div className="flex h-32 items-center justify-center">
              <Spinner />
            </div>
          ) : installations.length === 0 ? (
            <>
              <div className="rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 p-8 text-center">
                <p className="text-sm font-medium text-gray-600 dark:text-gray-400">{t("git.import.noAccounts.title")}</p>
                <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("git.import.noAccounts.hint")}</p>
                <div className="mt-4 flex justify-center gap-3">
                  <button
                    onClick={() => handleConnectProvider("github")}
                    disabled={connectingProvider !== null}
                    data-ux="git_import:connect_github"
                    className="rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    {t("git.import.connectGitHub")}
                  </button>
                  <button
                    onClick={() => setUrlDialogOpen(true)}
                    data-ux="git_import:connect_url_open"
                    className="rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-50"
                  >
                    {t("git.import.byUrl.open")}
                  </button>
                </div>
                {allowed && (
                  <button
                    type="button"
                    onClick={() => handleConnectProvider("github", true)}
                    disabled={connectingProvider !== null}
                    data-ux="git_import:open_github"
                    className="mt-3 inline-block text-xs font-medium text-blue-600 dark:text-blue-400 hover:underline disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    {t("git.import.openGithub")}
                  </button>
                )}
              </div>

              <div className="mt-6">
                <div className="mb-4 flex items-center gap-3">
                  <div className="h-px flex-1 bg-gray-200 dark:bg-gray-800" />
                  <span className="text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("git.import.orTemplate")}</span>
                  <div className="h-px flex-1 bg-gray-200 dark:bg-gray-800" />
                </div>
                <div className="space-y-4">
                  <div data-ux="git_import:upload_archive">
                    <UploadDeployCard projectId={projectId} envId={envId || null} />
                  </div>
                  <TemplateDeployCards projectId={projectId} envId={envId || null} placement="git-import" />
                </div>
              </div>
            </>
          ) : selectedRepo && !repoPickerOpen ? (
            // Compact selected-repo bar.
            <div className="flex items-center justify-between gap-3 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-4 py-3 shadow-sm">
              <div className="flex min-w-0 items-center gap-3">
                <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400">
                  <GithubMark className="h-4 w-4" />
                </span>
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-gray-900 dark:text-gray-100">{selectedRepo.full_name}</p>
                  <p className="truncate text-xs text-gray-400 dark:text-gray-500">
                    {selectedRepo.accountLogin} · {selectedRepo.default_branch || "main"}
                  </p>
                </div>
              </div>
              {!deploying && (
                <button
                  onClick={() => setRepoPickerOpen(true)}
                  className="shrink-0 rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-200 shadow-sm transition-colors hover:border-blue-400 hover:text-blue-600"
                >
                  {t("git.import.changeRepo")}
                </button>
              )}
            </div>
          ) : (
            <>
              <div className="flex flex-col gap-2">
                <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                  <div className="flex items-center rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 text-sm text-gray-700 dark:text-gray-200 shadow-sm sm:max-w-md">
                    <GithubMark className="mr-2 h-4 w-4 shrink-0 text-gray-500 dark:text-gray-400" />
                    <span className="truncate">
                      {installations.map((inst) => inst.account_login).join(", ")}
                    </span>
                  </div>
                  {!deploying && (
                    <div className="flex shrink-0 items-center gap-2">
                      <button
                        onClick={() => handleConnectProvider("github", true)}
                        disabled={connectingProvider !== null}
                        className="inline-flex items-center justify-center gap-2 rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 shadow-sm transition-colors hover:border-blue-400 hover:text-blue-600 disabled:cursor-not-allowed disabled:opacity-60"
                      >
                        <Plus className="h-4 w-4" />
                        <span>{t("git.import.connectAnotherGitHub")}</span>
                      </button>
                      <button
                        onClick={() => setUrlDialogOpen(true)}
                        data-ux="git_import:connect_url_open"
                        className="inline-flex items-center justify-center rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 shadow-sm transition-colors hover:border-blue-400 hover:text-blue-600"
                      >
                        {t("git.import.byUrl.open")}
                      </button>
                    </div>
                  )}
                </div>
                {!deploying && allowed && (
                  <button
                    type="button"
                    onClick={() => handleConnectProvider("github", true)}
                    disabled={connectingProvider !== null}
                    data-ux="git_import:open_github"
                    className="self-end text-xs font-medium text-blue-600 dark:text-blue-400 hover:underline disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    {t("git.import.openGithub")}
                  </button>
                )}
                <div className="relative flex-1">
                  <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400 dark:text-gray-500" />
                  <input
                    type="text"
                    value={repoQuery}
                    onChange={(e) => setRepoQuery(e.target.value)}
                    placeholder={t("git.import.searchPlaceholder")}
                    className="w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 py-2 pl-9 pr-3 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                  />
                </div>
              </div>

              {repoError && (
                <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">{repoError}</div>
              )}

              {reposUnavailable ? (
                <div className="rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-800 dark:text-amber-300">
                  {t("git.import.reposUnavailable")}
                </div>
              ) : loadingRepos ? (
                <div className="flex h-32 items-center justify-center">
                  <Spinner />
                </div>
              ) : remoteRepos.length === 0 ? (
                <p className="rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 p-8 text-center text-sm text-gray-500 dark:text-gray-400">
                  {t("git.import.noRepos")}
                </p>
              ) : filteredRepos.length === 0 ? (
                <p className="rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 p-8 text-center text-sm text-gray-500 dark:text-gray-400">
                  {t("git.import.noMatch")}
                </p>
              ) : (
                <div className="max-h-[420px] divide-y divide-gray-100 dark:divide-gray-800 overflow-y-auto rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 shadow-sm">
                  {filteredRepos.map((repo) => {
                    const shortName = repo.full_name.split("/").pop() || repo.full_name;
                    const isSel = selectedRepo?.full_name === repo.full_name;
                    return (
                      <div
                        key={repo.full_name}
                        className={`group flex items-center justify-between gap-3 px-4 py-3 transition-colors hover:bg-gray-50 ${isSel ? "bg-blue-50/50 dark:bg-blue-950/40" : ""}`}
                      >
                        <div className="flex min-w-0 items-center gap-3">
                          <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400">
                            <GithubMark className="h-4 w-4" />
                          </span>
                          <div className="min-w-0">
                            <div className="flex items-center gap-2">
                              <p className="truncate text-sm font-medium text-gray-900 dark:text-gray-100">{shortName}</p>
                              {repo.private && <Lock className="h-3 w-3 shrink-0 text-gray-400 dark:text-gray-500" />}
                            </div>
                            <p className="truncate text-xs text-gray-400 dark:text-gray-500">
                              {repo.accountLogin}{repo.updated_at ? ` · ${timeAgo(repo.updated_at)}` : ""}
                            </p>
                          </div>
                        </div>
                        <button
                          onClick={() => pickRepo(repo)}
                          className="shrink-0 rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-200 shadow-sm transition-colors hover:border-blue-400 hover:text-blue-600 group-hover:border-blue-300"
                        >
                          {isSel ? t("git.import.selectedButton") : t("git.import.importButton")}
                        </button>
                      </div>
                    );
                  })}
                </div>
              )}
            </>
          )}
        </section>

        {/* ── 2. Configure ── */}
        {selectedRepo && (
          <section className="space-y-4">
            <SectionHeader n={2} label={t("git.import.section.configure")} done={deploying} active={!deploying} />

            {ciWorkflows.length > 0 && (
              <div className="space-y-3 rounded-lg border border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/30 p-4">
                <div>
                  <p className="text-sm font-medium text-blue-800 dark:text-blue-200">{t("deployHooks.wizard.gha.title")}</p>
                  <p className="mt-1 text-sm text-blue-700 dark:text-blue-300">
                    {t("deployHooks.wizard.gha.body", { n: ciWorkflows.length })}
                  </p>
                  <p className="mt-1 font-mono text-xs text-blue-600 dark:text-blue-400">{ciWorkflows.join(", ")}</p>
                </div>

                <div className="flex flex-wrap items-center gap-4">
                  <button
                    type="button"
                    onClick={() => setGhaGuideOpen((v) => !v)}
                    className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700 transition-colors"
                  >
                    {ghaGuideOpen ? t("deployHooks.wizard.gha.hideGuide") : t("deployHooks.wizard.gha.showGuide")}
                  </button>

                  <div>
                    <button
                      type="button"
                      disabled
                      className="rounded-lg border border-blue-300 dark:border-blue-800 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm font-medium text-blue-700 dark:text-blue-300 opacity-50 cursor-not-allowed"
                    >
                      {t("deployHooks.wizard.gha.agentCta")}
                    </button>
                    <p className="mt-1 text-xs text-blue-600 dark:text-blue-400">{t("deployHooks.wizard.gha.agentSoon")}</p>
                  </div>
                </div>

                {ghaGuideOpen && (
                  <div className="space-y-3 border-t border-blue-200 dark:border-blue-900 pt-3">
                    <ol className="list-decimal space-y-1 pl-5 text-sm text-blue-800 dark:text-blue-200">
                      <li>{t("deployHooks.wizard.gha.step1")}</li>
                      <li>{t("deployHooks.wizard.gha.step2")}</li>
                      <li>{t("deployHooks.wizard.gha.step3")}</li>
                      <li>{t("deployHooks.wizard.gha.step4")}</li>
                    </ol>

                    <div className="relative">
                      <pre className="overflow-x-auto rounded-lg border border-gray-800 bg-gray-900 p-3 pr-20 font-mono text-xs text-gray-100">
                        {deployCurl("https://console.dada-tuda.ru")}
                      </pre>
                      <div className="absolute right-2 top-2">
                        <CopyButton value={deployCurl("https://console.dada-tuda.ru")} label={t("common.copy")} />
                      </div>
                    </div>

                    <p className="text-xs text-blue-700 dark:text-blue-300">{t("deployHooks.wizard.gha.actionAlt")}</p>
                    <div className="relative">
                      <pre className="overflow-x-auto rounded-lg border border-gray-800 bg-gray-900 p-3 pr-20 font-mono text-xs text-gray-100">
                        {githubActionsStep("https://console.dada-tuda.ru")}
                      </pre>
                      <div className="absolute right-2 top-2">
                        <CopyButton value={githubActionsStep("https://console.dada-tuda.ru")} label={t("common.copy")} />
                      </div>
                    </div>
                  </div>
                )}
              </div>
            )}

            <form onSubmit={handleSubmit} className="space-y-5">
              <fieldset disabled={deploying} className="space-y-5 disabled:opacity-60">
                <div className="rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-4 py-3">
                  <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("git.import.detectedFramework")}</p>
                  {detecting ? (
                    <div className="mt-2 flex items-center gap-2 text-sm text-gray-500 dark:text-gray-400">
                      <Spinner size="sm" /> {t("git.import.detecting")}
                    </div>
                  ) : detectError ? (
                    <div className="mt-2 flex items-center justify-between gap-3 text-sm">
                      <span className="text-amber-600 dark:text-amber-500">{t("git.import.detectFailed")}</span>
                      <button
                        type="button"
                        onClick={() => selectedRepo && runDetect(selectedRepo, rootDir)}
                        className="shrink-0 rounded-md border border-gray-300 dark:border-gray-700 px-2 py-1 text-xs font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-50 dark:hover:bg-gray-800"
                      >
                        {t("git.import.detectRetry")}
                      </button>
                    </div>
                  ) : detection ? (
                    <div className="mt-2 space-y-1 text-sm">
                      <p className="flex items-center gap-2 font-medium text-gray-900 dark:text-gray-100">
                        <FrameworkLogo id={detectedPresetId(detection.framework)} className="h-5 w-5" />
                        {frameworkLabel(detection.framework) || t("git.import.unknownFramework")}
                      </p>
                      {detection.build_command && (
                        <p className="text-xs text-gray-500 dark:text-gray-400">build: <span className="font-mono">{detection.build_command}</span></p>
                      )}
                      {detection.install_command && (
                        <p className="text-xs text-gray-500 dark:text-gray-400">install: <span className="font-mono">{detection.install_command}</span></p>
                      )}
                      {detection.package_manager && (
                        <p className="text-xs text-gray-500 dark:text-gray-400">pm: <span className="font-mono">{detection.package_manager}</span></p>
                      )}
                      {detection.start_command && (
                        <p className="text-xs text-gray-500 dark:text-gray-400">start: <span className="font-mono">{detection.start_command}</span></p>
                      )}
                      {detection.output_dir && (
                        <p className="text-xs text-gray-500 dark:text-gray-400">output: <span className="font-mono">{detection.output_dir}</span></p>
                      )}
                      {typeof detection.port === "number" && (
                        <p className="text-xs text-gray-500 dark:text-gray-400">port: <span className="font-mono">{detection.port}</span></p>
                      )}
                    </div>
                  ) : null}
                </div>

                <div>
                  <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
                    {t("git.import.appName.label")} <span className="font-normal text-gray-400 dark:text-gray-500">{t("git.import.appName.hint")}</span>
                  </label>
                  <input
                    type="text"
                    required
                    value={appName}
                    onChange={(e) => setAppName(toKubeName(e.target.value))}
                    placeholder={t("git.import.appName.placeholder")}
                    pattern="[a-z0-9-]+"
                    className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                  />
                  <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("git.import.appName.help")}</p>
                </div>

                <div>
                  <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("git.import.framework.label")}</label>
                  <div className="mt-1">
                    <Select
                      value={frameworkOverride || "auto"}
                      onValueChange={(raw) => {
                        const id = raw === "auto" ? "" : raw;
                        setFrameworkOverride(id);
                        setFrameworkTouched(id !== "");
                        setPortTouched(false);
                        const preset = PRESET_BY_ID.get(id);
                        if (preset) setPort(preset.port);
                      }}
                    >
                      <SelectTrigger className="h-auto w-full px-3 py-2 text-sm [&>span]:flex [&>span]:items-center [&>span]:gap-2">
                        <SelectValue placeholder={t("git.import.framework.auto")} />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="auto">{t("git.import.framework.auto")}</SelectItem>
                        {FRAMEWORK_PRESETS.map((g) => (
                          <SelectGroup key={g.group}>
                            <SelectLabel>{g.group}</SelectLabel>
                            {g.items.map((p) => (
                              <SelectItem key={p.id} value={p.id}>
                                <span className="flex items-center gap-2">
                                  <FrameworkLogo id={p.id} className="h-4 w-4" />
                                  {p.label}
                                </span>
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                  <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("git.import.framework.hint")}</p>
                </div>

                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("git.import.port.label")}</label>
                    <input
                      type="number"
                      required={!worker}
                      disabled={worker}
                      min={1}
                      max={65535}
                      value={worker ? "" : port}
                      onChange={(e) => {
                        setPortTouched(true);
                        setPort(parseInt(e.target.value, 10) || 8080);
                      }}
                      className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 disabled:cursor-not-allowed disabled:bg-gray-50 dark:disabled:bg-gray-900"
                    />
                  </div>
                  <div>
                    <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("git.import.profile.label")}</label>
                    <select
                      value={profile}
                      onChange={(e) => setProfile(e.target.value)}
                      className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                    >
                      <option value="small">small</option>
                      <option value="medium">medium</option>
                      <option value="large">large</option>
                    </select>
                  </div>
                </div>

                <label className="flex items-start gap-3 rounded-lg border border-gray-200 dark:border-gray-800 px-4 py-3">
                  <input
                    type="checkbox"
                    checked={worker}
                    onChange={(e) => setWorker(e.target.checked)}
                    className="mt-0.5 h-4 w-4 rounded border-gray-300 dark:border-gray-700"
                  />
                  <span>
                    <span className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("git.import.worker.label")}</span>
                    <span className="mt-0.5 block text-xs text-gray-400 dark:text-gray-500">{t("git.import.worker.hint")}</span>
                  </span>
                </label>

                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("git.import.branch.label")}</label>
                    <input
                      type="text"
                      required
                      value={branch}
                      onChange={(e) => setBranch(e.target.value)}
                      placeholder="main"
                      className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                    />
                  </div>
                  <div>
                    <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("git.import.rootDir.label")}</label>
                    <input
                      type="text"
                      value={rootDir}
                      onChange={(e) => setRootDir(e.target.value)}
                      onBlur={() => selectedRepo && runDetect(selectedRepo, rootDir)}
                      placeholder="."
                      className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                    />
                  </div>
                </div>

                <div className="flex items-center justify-between rounded-lg border border-gray-200 dark:border-gray-800 px-4 py-3">
                  <div>
                    <p className="text-sm font-medium text-gray-700 dark:text-gray-200">{t("git.import.autoDeploy.label")}</p>
                    <p className="text-xs text-gray-400 dark:text-gray-500">{t("git.import.autoDeploy.hint")}</p>
                  </div>
                  <button
                    type="button"
                    onClick={() => setAutoDeploy((v) => !v)}
                    className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 ${
                      autoDeploy ? "bg-blue-600" : "bg-gray-200 dark:bg-gray-700"
                    }`}
                    role="switch"
                    aria-checked={autoDeploy}
                  >
                    <span className={`inline-block h-4 w-4 transform rounded-full bg-white dark:bg-gray-900 shadow transition-transform ${autoDeploy ? "translate-x-6" : "translate-x-1"}`} />
                  </button>
                </div>
              </fieldset>

              {submitError && (
                <div role="alert" className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
                  {submitError}
                  {githubAccessRequired && (
                    <div className="mt-2">
                      <button
                        type="button"
                        data-ux="git_import:github_access_required_connect"
                        onClick={() => void handleConnectProvider("github", true)}
                        disabled={connectingProvider !== null}
                        className="inline-flex items-center gap-1.5 rounded-md bg-red-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-red-700 disabled:opacity-60"
                      >
                        {t("git.import.error.githubAccessRequired.cta")}
                      </button>
                    </div>
                  )}
                </div>
              )}

              {!deploying && (
                <div className="flex justify-end gap-3 pt-1">
                  <Link
                    href={`/projects/${projectId}/git${envId ? `?envId=${envId}` : ""}`}
                    onClick={() => clearDraft(projectId)}
                    className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 transition-colors"
                  >
                    {t("common.cancel")}
                  </Link>
                  <button
                    type="submit"
                    disabled={submitting || !appName}
                    className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
                  >
                    {t("git.import.deploy.button")}
                  </button>
                </div>
              )}
            </form>
          </section>
        )}

        {/* ── 3. Deploy ── */}
        {deploying && selectedRepo && (
          <section className="space-y-4">
            <SectionHeader
              n={3}
              label={t("git.import.section.deploy")}
              done={build?.status === "success"}
              active={build?.status !== "success"}
            />

            <div className="rounded-lg border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900 px-4 py-3">
              <div className="flex items-center justify-between gap-3">
                <div className="min-w-0">
                  <p className="truncate font-mono text-sm font-medium text-gray-900 dark:text-gray-100">{appName}</p>
                  <p className="truncate text-xs text-gray-400 dark:text-gray-500">{selectedRepo.full_name}</p>
                </div>
                {build ? (
                  <BuildStatusBadge status={build.status} />
                ) : deployError ? null : (
                  <span className="inline-flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
                    <Spinner size="sm" /> {t("git.import.deploy.starting")}
                  </span>
                )}
              </div>
            </div>

            {deployError ? (
              <div role="alert" className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
                {deployError}
              </div>
            ) : build ? (
              <BuildLogViewer projectId={projectId} buildId={build.id} />
            ) : (
              <div className="flex h-32 items-center justify-center rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900">
                <Spinner />
              </div>
            )}

            {build && build.status === "success" && (
              <div className="rounded-lg border border-green-200 dark:border-green-900 bg-green-50 dark:bg-green-950/40 px-4 py-3 text-sm text-green-800 dark:text-green-300">
                {t("git.import.deploy.success")}
              </div>
            )}

            <div className="flex flex-wrap justify-end gap-3 pt-1">
              {(deployError || (build && (build.status === "failed" || build.status === "canceled"))) && (
                <button
                  onClick={triggerDeploy}
                  className="rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-50 transition-colors"
                >
                  {t("git.import.deploy.retry")}
                </button>
              )}
              <Link
                href={`/projects/${projectId}/apps/${appName}/deployments${envId ? `?envId=${envId}` : ""}`}
                onClick={() => clearDraft(projectId)}
                className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 transition-colors"
              >
                {t("git.import.deploy.viewDeployments")}
              </Link>
              {build && build.status === "success" && (
                <Link
                  href={`/projects/${projectId}/apps/${appName}${envId ? `?envId=${envId}` : ""}`}
                  onClick={() => clearDraft(projectId)}
                  className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 transition-colors"
                >
                  {t("git.import.deploy.openApp")}
                </Link>
              )}
            </div>
          </section>
        )}
      </div>

      <ConnectByUrlDialog
        projectId={projectId}
        envId={envId || null}
        open={urlDialogOpen}
        onClose={() => setUrlDialogOpen(false)}
      />
    </div>
  );
}

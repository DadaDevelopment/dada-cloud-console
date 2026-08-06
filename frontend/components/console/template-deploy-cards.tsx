"use client";
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { gitApi, buildsApi, solutionsApi } from "@/lib/api";
import type { Solution } from "@/lib/types";
import { ResourceIcon } from "@/components/shell/icons";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";
import { trackBuildStart } from "@/lib/build-watch";

function toKubeName(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "")
    .slice(0, 40);
}

/** Random suffix so deploying the same project twice never collides on a name. */
function uniqueAppName(base: string): string {
  const suffix = Math.random().toString(36).slice(2, 8);
  return toKubeName(`${toKubeName(base).slice(0, 30)}-${suffix}`);
}

export interface TemplateDeployCardsProps {
  projectId: string;
  envId: string | null;
  /** Denser layout for secondary placements (apps empty state, git import wall). */
  compact?: boolean;
  /**
   * Prominent first-run treatment: bigger card, bigger heading, activation
   * copy. Used where this is the primary onramp (empty-project overview,
   * apps empty state) rather than a secondary option next to Git.
   */
  hero?: boolean;
  className?: string;
}

/**
 * No-GitHub escape hatch: builds and deploys a real open-source project — or
 * any public repository the visitor pastes — without connecting a git account.
 *
 * Both paths run the ordinary customer flow (link the public repo, build it,
 * deploy the image), which is the point: what the visitor sees on the empty
 * screen is the same machinery their own first repository will go through. The
 * catalog comes from the backend rather than a list in this file, so adding a
 * project is a backend change and the console never disagrees with it about
 * which branch or port an entry builds with.
 */
export function TemplateDeployCards({ projectId, envId, compact, hero, className }: TemplateDeployCardsProps) {
  const { t } = useT();
  const router = useRouter();
  const [solutions, setSolutions] = useState<Solution[] | null>(null);
  const [deployingKey, setDeployingKey] = useState<string | null>(null);
  const [templateError, setTemplateError] = useState<string | null>(null);
  const [repoUrl, setRepoUrl] = useState("");
  const [repoBranch, setRepoBranch] = useState("");

  useEffect(() => {
    let cancelled = false;
    solutionsApi
      .list()
      .then((res) => {
        if (!cancelled) setSolutions(res.solutions ?? []);
      })
      .catch(() => {
        if (!cancelled) setSolutions([]);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  /**
   * Links a public repo and starts its first build. `installation_id: ""` is
   * what makes this work without a connected GitHub account — the clone URL is
   * derived from the repository name and the repo is public.
   */
  async function deploy(opts: {
    key: string;
    appBase: string;
    repoFullName: string;
    branch: string;
    rootDir: string;
    framework?: string;
    port?: number;
    profile?: string;
  }) {
    if (!envId || deployingKey) return;
    setTemplateError(null);
    setDeployingKey(opts.key);
    const appName = uniqueAppName(opts.appBase);
    try {
      await gitApi.linkRepo(projectId, envId, {
        installation_id: "",
        repo_full_name: opts.repoFullName,
        app_name: appName,
        production_branch: opts.branch,
        root_dir: opts.rootDir,
        framework_override: opts.framework,
        auto_deploy: false,
        port: opts.port,
        profile: opts.profile ?? "small",
      });
      const { build } = await buildsApi.trigger(projectId, envId, appName);
      if (build?.id) trackBuildStart({ projectId, envId, appName, buildId: build.id });
      router.push(`/projects/${projectId}/apps/${appName}/deployments?envId=${envId}`);
    } catch (err) {
      setTemplateError(err instanceof Error ? err.message : t("overview.templates.error"));
      setDeployingKey(null);
    }
  }

  function deploySolution(s: Solution) {
    return deploy({
      key: s.slug,
      appBase: s.slug,
      repoFullName: s.repo,
      branch: s.branch,
      rootDir: s.root_dir,
      framework: s.framework,
      port: s.port,
      profile: s.profile,
    });
  }

  /**
   * The paste-a-link path. Detection runs before the link so the app is created
   * with the port the repository actually listens on: a wrong port deploys
   * green and answers 502, which reads as the platform being broken.
   */
  async function deployPastedRepo() {
    if (!envId || deployingKey || !repoUrl.trim()) return;
    setTemplateError(null);
    setDeployingKey("__url__");
    try {
      const { repo_full_name } = await solutionsApi.parseRepoUrl(repoUrl);
      let framework: string | undefined;
      let port: number | undefined;
      try {
        const detected = await gitApi.detectPublic(projectId, repo_full_name);
        framework = detected.framework ?? undefined;
        port = detected.port ?? undefined;
      } catch {
        // Detection is best effort: the build pipeline detects again on the
        // real checkout, which sees more than the GitHub API does.
      }
      setDeployingKey(null);
      await deploy({
        key: "__url__",
        appBase: repo_full_name.split("/")[1] ?? "app",
        repoFullName: repo_full_name,
        // Empty means "main" server-side. The field is here because plenty of
        // real repositories still default to master, and guessing the branch
        // wrong fails the build with an error about a missing ref rather than
        // about the guess.
        branch: repoBranch.trim(),
        rootDir: ".",
        framework,
        port,
      });
    } catch (err) {
      setTemplateError(err instanceof Error ? err.message : t("overview.templates.error"));
      setDeployingKey(null);
    }
  }

  const busyUrl = deployingKey === "__url__";

  const body = (
    <>
      {!compact && (
        <div className="mb-1">
          <h2
            className={
              hero
                ? "text-lg font-bold text-gray-900 dark:text-gray-100 sm:text-xl"
                : "text-sm font-semibold text-gray-900 dark:text-gray-100"
            }
          >
            {hero ? t("overview.templates.heroTitle") : t("overview.templates.title")}
          </h2>
          <p
            className={
              hero
                ? "mt-2 text-sm text-gray-600 dark:text-gray-300 sm:text-base"
                : "mt-1 text-sm text-gray-500 dark:text-gray-400"
            }
          >
            {hero ? t("overview.templates.heroHint") : t("overview.templates.hint")}
          </p>
        </div>
      )}
      {templateError && (
        <div className="mt-3 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-3 py-2 text-sm text-red-700 dark:text-red-300">
          {templateError}
        </div>
      )}

      <div className={`grid gap-4 sm:grid-cols-2 lg:grid-cols-3 ${compact ? "" : "mt-4"}`}>
        {solutions === null
          ? [0, 1, 2].map((i) => <SolutionCardSkeleton key={i} />)
          : solutions.map((s) => (
              <SolutionCard
                key={s.slug}
                solution={s}
                cta={deployingKey === s.slug ? t("overview.templates.deploying") : t("overview.templates.cta")}
                busy={deployingKey === s.slug}
                disabled={!!deployingKey || !envId}
                onClick={() => deploySolution(s)}
              />
            ))}
      </div>

      <div className="mt-5 rounded-xl border border-dashed border-gray-300 dark:border-gray-700 p-4">
        <p className="text-sm font-semibold text-gray-900 dark:text-gray-100">
          {t("overview.templates.byUrl.title")}
        </p>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          {t("overview.templates.byUrl.hint")}
        </p>
        <div className="mt-3 flex flex-col gap-2 sm:flex-row">
          <input
            type="url"
            inputMode="url"
            value={repoUrl}
            onChange={(e) => setRepoUrl(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") void deployPastedRepo();
            }}
            placeholder={t("overview.templates.byUrl.placeholder")}
            disabled={!!deployingKey || !envId}
            className="flex-1 rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 placeholder:text-gray-400 disabled:opacity-60"
          />
          <input
            type="text"
            value={repoBranch}
            onChange={(e) => setRepoBranch(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") void deployPastedRepo();
            }}
            placeholder={t("overview.templates.byUrl.branchPlaceholder")}
            aria-label={t("overview.templates.byUrl.branchLabel")}
            disabled={!!deployingKey || !envId}
            className="rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 placeholder:text-gray-400 disabled:opacity-60 sm:w-40"
          />
          <button
            type="button"
            onClick={() => void deployPastedRepo()}
            disabled={!!deployingKey || !envId || !repoUrl.trim()}
            className="inline-flex items-center justify-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-60"
          >
            {busyUrl && <Spinner size="sm" />}
            {busyUrl ? t("overview.templates.deploying") : t("overview.templates.byUrl.cta")}
          </button>
        </div>
      </div>
    </>
  );

  if (compact) {
    return <div className={className}>{body}</div>;
  }

  const containerClass = hero
    ? "rounded-2xl border-2 border-blue-200 dark:border-blue-900/60 bg-gradient-to-br from-blue-50 to-white dark:from-blue-950/20 dark:to-gray-900 p-6 shadow-sm sm:p-8"
    : "rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm";

  return <div className={`${containerClass} ${className ?? ""}`}>{body}</div>;
}

function SolutionCardSkeleton() {
  return (
    <div className="flex flex-col rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
      <div className="mb-4 h-10 w-10 animate-pulse rounded-lg bg-gray-100 dark:bg-gray-800" />
      <div className="h-4 w-24 animate-pulse rounded bg-gray-100 dark:bg-gray-800" />
      <div className="mt-2 h-3 w-full animate-pulse rounded bg-gray-100 dark:bg-gray-800" />
      <div className="mt-1 h-3 w-2/3 animate-pulse rounded bg-gray-100 dark:bg-gray-800" />
      <div className="mt-4 h-9 w-full animate-pulse rounded-lg bg-gray-100 dark:bg-gray-800" />
    </div>
  );
}

function SolutionCard({
  solution,
  cta,
  busy,
  disabled,
  onClick,
}: {
  solution: Solution;
  cta: string;
  busy: boolean;
  disabled: boolean;
  onClick: () => void;
}) {
  return (
    <div className="flex flex-col rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
      <div className="mb-4 flex h-10 w-10 items-center justify-center rounded-lg bg-blue-100 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400">
        <ResourceIcon name="apps" className="h-5 w-5" />
      </div>
      <p className="text-sm font-semibold text-gray-900 dark:text-gray-100">{solution.name}</p>
      <p className="mt-1 flex-1 text-sm text-gray-500 dark:text-gray-400">{solution.tagline}</p>
      <p className="mt-2 truncate text-xs text-gray-400 dark:text-gray-500" title={solution.repo}>
        {solution.repo} · {solution.license}
      </p>
      <button
        type="button"
        onClick={onClick}
        disabled={disabled}
        className="mt-4 inline-flex items-center justify-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-60"
      >
        {busy && <Spinner size="sm" />}
        {cta}
      </button>
    </div>
  );
}

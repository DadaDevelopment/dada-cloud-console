"use client";
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { gitApi, solutionsApi } from "@/lib/api";
import type { Solution, SolutionCandidate } from "@/lib/types";
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
  const [query, setQuery] = useState("");
  const [candidates, setCandidates] = useState<SolutionCandidate[] | null>(null);
  const [resolving, setResolving] = useState(false);
  const [searchFailed, setSearchFailed] = useState(false);

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
   * Resolves what the customer typed, one debounced request per pause in the
   * typing. The delay is not cosmetic: every keystroke that reaches the backend
   * can become a GitHub search, and that budget is 30 requests a minute for the
   * whole platform, not per customer.
   */
  useEffect(() => {
    const typed = query.trim();
    if (typed.length < 2) return;
    let cancelled = false;
    const timer = setTimeout(() => {
      setResolving(true);
      solutionsApi
        .resolve(projectId, typed)
        .then((res) => {
          if (cancelled) return;
          setCandidates(res.candidates ?? []);
          setSearchFailed(res.search_failed);
        })
        .catch(() => {
          if (!cancelled) {
            setCandidates([]);
            setSearchFailed(true);
          }
        })
        .finally(() => {
          if (!cancelled) setResolving(false);
        });
    }, 350);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [query, projectId]);

  /**
   * Installs one project: a single call that links the public repository,
   * orders any managed database the catalog entry declares it needs, and
   * queues the first build. No connected GitHub account is required — the
   * repository is public and the backend derives the clone URL from its name.
   *
   * The app name is minted here rather than server-side so deploying the same
   * project twice never collides on a name.
   */
  async function deploy(opts: {
    key: string;
    appBase: string;
    slug?: string;
    repoFullName?: string;
    branch?: string;
    rootDir?: string;
    framework?: string;
    port?: number;
    profile?: string;
  }) {
    if (!envId || deployingKey) return;
    setTemplateError(null);
    setDeployingKey(opts.key);
    const appName = uniqueAppName(opts.appBase);
    try {
      const { build } = await solutionsApi.install(projectId, envId, {
        slug: opts.slug,
        repo: opts.repoFullName,
        app_name: appName,
        branch: opts.branch,
        root_dir: opts.rootDir,
        framework: opts.framework,
        port: opts.port,
        profile: opts.profile,
      });
      if (build?.id) trackBuildStart({ projectId, envId, appName, buildId: build.id });
      router.push(`/projects/${projectId}/apps/${appName}/deployments?envId=${envId}`);
    } catch (err) {
      setTemplateError(err instanceof Error ? err.message : t("overview.templates.error"));
      setDeployingKey(null);
    }
  }

  function deploySolution(s: Solution) {
    return deploy({ key: s.slug, appBase: s.slug, slug: s.slug });
  }

  /**
   * Deploys one resolver row.
   *
   * A catalog row already carries the build spec we verified, so it goes
   * straight through. A repository row — pasted or found by search — carries a
   * name and nothing else, so detection runs first: the port a repository
   * actually listens on is what separates an app that answers from one that
   * deploys green and returns 502, which reads as the platform being broken.
   * A managed row is not a build at all and hands over to the databases page,
   * where the customer picks size and backups.
   */
  async function deployCandidate(c: SolutionCandidate) {
    if (!envId || deployingKey) return;
    if (c.kind === "managed") {
      router.push(`/projects/${projectId}/databases?envId=${envId}`);
      return;
    }
    if (c.kind === "solution") {
      await deploy({ key: `cand:${c.slug}`, appBase: c.slug, slug: c.slug });
      return;
    }
    const key = `cand:${c.repo}`;
    setTemplateError(null);
    setDeployingKey(key);
    let framework = c.framework || undefined;
    let port = c.port || undefined;
    try {
      const detected = await gitApi.detectPublic(projectId, c.repo);
      framework = detected.framework ?? framework;
      port = detected.port ?? port;
    } catch {
      /* Best effort: the build pipeline detects again on the real checkout, which sees more than the GitHub API does. */
    }
    setDeployingKey(null);
    await deploy({
      key,
      appBase: c.repo.split("/")[1] ?? "app",
      repoFullName: c.repo,
      branch: c.branch,
      rootDir: c.root_dir || ".",
      framework,
      port,
    });
  }

  const asking = query.trim().length >= 2;

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
          {t("overview.templates.ask.title")}
        </p>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          {t("overview.templates.ask.hint")}
        </p>
        <div className="mt-3 flex items-center gap-2 rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 focus-within:border-blue-500">
          <ResourceIcon name="apps" className="h-4 w-4 shrink-0 text-gray-400" />
          <input
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("overview.templates.ask.placeholder")}
            disabled={!!deployingKey || !envId}
            aria-label={t("overview.templates.ask.title")}
            className="flex-1 bg-transparent text-sm text-gray-900 dark:text-gray-100 placeholder:text-gray-400 focus:outline-none disabled:opacity-60"
          />
          {asking && resolving && <Spinner size="sm" />}
        </div>

        {asking && searchFailed && (
          <p className="mt-2 text-xs text-amber-600 dark:text-amber-400">
            {t("overview.templates.ask.searchFailed")}
          </p>
        )}

        {asking && candidates !== null && candidates.length === 0 && !resolving && (
          <p className="mt-3 text-sm text-gray-500 dark:text-gray-400">
            {t("overview.templates.ask.empty")}
          </p>
        )}

        {asking && candidates !== null && candidates.length > 0 && (
          <ul className="mt-3 divide-y divide-gray-100 dark:divide-gray-800 overflow-hidden rounded-lg border border-gray-200 dark:border-gray-800">
            {candidates.map((c) => (
              <CandidateRow
                key={`${c.kind}:${c.slug}`}
                candidate={c}
                busy={deployingKey === `cand:${c.kind === "solution" ? c.slug : c.repo}`}
                disabled={!!deployingKey || !envId}
                cta={
                  c.kind === "managed"
                    ? t("overview.templates.ask.openDatabases")
                    : t("overview.templates.cta")
                }
                badge={
                  c.kind === "managed"
                    ? t("overview.templates.ask.managed")
                    : c.kind === "solution"
                      ? t("overview.templates.ask.fromCatalog")
                      : c.from === "search"
                        ? t("overview.templates.ask.fromSearch")
                        : t("overview.templates.ask.fromLink")
                }
                archivedLabel={t("overview.templates.ask.archived")}
                onClick={() => void deployCandidate(c)}
              />
            ))}
          </ul>
        )}
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

/**
 * One resolver suggestion. The badge is the honest part: a catalog row carries
 * a build spec someone verified, a search row carries a name and a star count,
 * and the customer choosing between them deserves to know which is which.
 */
function CandidateRow({
  candidate,
  busy,
  disabled,
  cta,
  badge,
  archivedLabel,
  onClick,
}: {
  candidate: SolutionCandidate;
  busy: boolean;
  disabled: boolean;
  cta: string;
  badge: string;
  archivedLabel: string;
  onClick: () => void;
}) {
  return (
    <li className="flex items-center gap-3 bg-white dark:bg-gray-900 px-3 py-2.5">
      {candidate.icon ? (
        <img
          src={candidate.icon}
          alt=""
          className="h-8 w-8 shrink-0 rounded-md bg-gray-100 dark:bg-gray-800 object-cover"
        />
      ) : (
        <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-blue-100 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400">
          <ResourceIcon name={candidate.kind === "managed" ? "databases" : "apps"} className="h-4 w-4" />
        </div>
      )}
      <div className="min-w-0 flex-1">
        <p className="flex items-center gap-2 text-sm font-medium text-gray-900 dark:text-gray-100">
          <span className="truncate">{candidate.name}</span>
          <span className="shrink-0 rounded bg-gray-100 dark:bg-gray-800 px-1.5 py-0.5 text-[11px] font-normal text-gray-500 dark:text-gray-400">
            {badge}
          </span>
          {candidate.archived && (
            <span className="shrink-0 rounded bg-amber-100 dark:bg-amber-950/40 px-1.5 py-0.5 text-[11px] font-normal text-amber-700 dark:text-amber-400">
              {archivedLabel}
            </span>
          )}
        </p>
        <p className="truncate text-xs text-gray-500 dark:text-gray-400">
          {candidate.tagline || candidate.repo}
        </p>
      </div>
      {typeof candidate.stars === "number" && candidate.stars > 0 && (
        <span className="shrink-0 text-xs text-gray-400 dark:text-gray-500">★ {candidate.stars}</span>
      )}
      <button
        type="button"
        onClick={onClick}
        disabled={disabled}
        className="inline-flex shrink-0 items-center justify-center gap-1.5 rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm font-medium text-gray-900 dark:text-gray-100 transition-colors hover:bg-gray-50 dark:hover:bg-gray-800 disabled:cursor-not-allowed disabled:opacity-60"
      >
        {busy && <Spinner size="sm" />}
        {cta}
      </button>
    </li>
  );
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

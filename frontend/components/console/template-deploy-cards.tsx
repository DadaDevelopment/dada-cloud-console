"use client";
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { gitApi, solutionsApi } from "@/lib/api";
import type { Solution, SolutionCandidate, SolutionCategory } from "@/lib/types";
import { ResourceIcon } from "@/components/shell/icons";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";
import { trackBuildStart } from "@/lib/build-watch";
import { templateUxName, type TemplateDeployPlacement } from "@/lib/ux-target-names";

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

/** How many catalog chips the compact placement shows before «show all». */
const CHIP_LIMIT = 8;

type Translate = (key: string, vars?: Record<string, string | number>) => string;

export type { TemplateDeployPlacement };
export { templateUxName };

export interface TemplateDeployCardsProps {
  projectId: string;
  envId: string | null;
  placement: TemplateDeployPlacement;
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
export function TemplateDeployCards({ projectId, envId, placement, compact, hero, className }: TemplateDeployCardsProps) {
  const { t } = useT();
  const router = useRouter();
  const [solutions, setSolutions] = useState<Solution[] | null>(null);
  const [categories, setCategories] = useState<SolutionCategory[]>([]);
  const [showAll, setShowAll] = useState(false);
  const [paramsFor, setParamsFor] = useState<Solution | null>(null);
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
        if (cancelled) return;
        setSolutions(res.solutions ?? []);
        setCategories(res.categories ?? []);
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
   *
   * DESTINATION. The app overview, not `/deployments`. The deployments feed
   * carries no live-URL surface, so the dominant new-user path never saw one;
   * overview shows the live URL, polls the app phase and fires the Metrika
   * deploy-success goal. The app row lags the build trigger, and overview
   * tolerates that by retrying `appsApi.list` for ~120s before not-found.
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
    params?: Record<string, string>;
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
        params: opts.params,
      });
      if (build?.id) trackBuildStart({ projectId, envId, appName, buildId: build.id });
      router.push(`/projects/${projectId}/apps/${appName}?envId=${envId}`);
    } catch (err) {
      setTemplateError(err instanceof Error ? err.message : t("overview.templates.error"));
      setDeployingKey(null);
    }
  }

  /**
   * An entry that asks for a password or an endpoint cannot be a one-click
   * chip: installing it blind would either fail on a required parameter or,
   * worse, come up with no password on a public address. Those open the form
   * first; everything else deploys on the click.
   */
  function deploySolution(s: Solution) {
    if (s.params && s.params.length > 0) {
      setParamsFor(s);
      return;
    }
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

  const shownChips = showAll ? (solutions ?? []) : (solutions ?? []).slice(0, CHIP_LIMIT);

  /**
   * Cards grouped by the categories the backend named, in its order. An entry
   * whose category nothing titles would otherwise vanish from the grid, so the
   * leftovers get their own group at the end rather than being dropped.
   */
  const grouped = (() => {
    const rest = new Set((solutions ?? []).map((s) => s.slug));
    const out: { category: SolutionCategory; items: Solution[] }[] = [];
    for (const category of categories) {
      const items = (solutions ?? []).filter((s) => s.category === category.key);
      items.forEach((s) => rest.delete(s.slug));
      if (items.length > 0) out.push({ category, items });
    }
    const leftovers = (solutions ?? []).filter((s) => rest.has(s.slug));
    if (leftovers.length > 0) {
      out.push({ category: { key: "other", title: "" }, items: leftovers });
    }
    return out;
  })();

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

      {compact ? (
        <div className="flex flex-wrap gap-2">
          {solutions === null
            ? [0, 1, 2, 3].map((i) => <SolutionChipSkeleton key={i} />)
            : shownChips.map((s) => (
                <SolutionChip
                  key={s.slug}
                  solution={s}
                  busy={deployingKey === s.slug}
                  disabled={!!deployingKey || !envId}
                  uxName={templateUxName(placement, "deploy", s.slug)}
                  onClick={() => deploySolution(s)}
                />
              ))}
          {solutions !== null && solutions.length > CHIP_LIMIT && (
            <button
              type="button"
              onClick={() => setShowAll((v) => !v)}
              className="inline-flex items-center rounded-full border border-dashed border-gray-300 dark:border-gray-700 px-3.5 py-1.5 text-sm font-medium text-gray-600 dark:text-gray-300 transition-colors hover:border-blue-400 hover:text-blue-600"
            >
              {showAll
                ? t("overview.templates.showLess")
                : t("overview.templates.showAll", { count: solutions.length })}
            </button>
          )}
        </div>
      ) : solutions === null ? (
        <div className="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {[0, 1, 2].map((i) => (
            <SolutionCardSkeleton key={i} />
          ))}
        </div>
      ) : (
        <div className="mt-4 space-y-6">
          {grouped.map(({ category, items }) => (
            <div key={category.key}>
              <p className="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
                {category.title}
              </p>
              <div className="mt-3 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                {items.map((s) => (
                  <SolutionCard
                    key={s.slug}
                    solution={s}
                    cta={deployingKey === s.slug ? t("overview.templates.deploying") : t("overview.templates.cta")}
                    busy={deployingKey === s.slug}
                    disabled={!!deployingKey || !envId}
                    uxName={templateUxName(placement, "deploy", s.slug)}
                    onClick={() => deploySolution(s)}
                    t={t}
                  />
                ))}
              </div>
            </div>
          ))}
        </div>
      )}

      {paramsFor && (
        <SolutionParamsDialog
          solution={paramsFor}
          busy={deployingKey === paramsFor.slug}
          t={t}
          onCancel={() => setParamsFor(null)}
          onSubmit={(values) => {
            const s = paramsFor;
            setParamsFor(null);
            void deploy({ key: s.slug, appBase: s.slug, slug: s.slug, params: values });
          }}
        />
      )}

      <div
        className={
          compact
            ? "mt-4"
            : "mt-5 rounded-xl border border-dashed border-gray-300 dark:border-gray-700 p-4"
        }
      >
        <p className="text-sm font-semibold text-gray-900 dark:text-gray-100">
          {compact ? t("overview.templates.ask.anythingTitle") : t("overview.templates.ask.title")}
        </p>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          {t("overview.templates.ask.hint")}
        </p>
        <div
          className={`mt-3 flex items-center gap-2 rounded-lg border bg-white dark:bg-gray-900 focus-within:border-blue-500 ${
            compact
              ? "border-gray-300 dark:border-gray-700 px-3 py-2.5 shadow-sm"
              : "border-gray-300 dark:border-gray-700 px-3 py-2"
          }`}
        >
          {compact ? (
            <GithubMark className="h-4 w-4 shrink-0 text-gray-400" />
          ) : (
            <ResourceIcon name="apps" className="h-4 w-4 shrink-0 text-gray-400" />
          )}
          <input
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("overview.templates.ask.placeholder")}
            disabled={!!deployingKey || !envId}
            aria-label={t("overview.templates.ask.title")}
            data-ux={templateUxName(placement, "ask")}
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
                uxName={templateUxName(placement, "candidate", c.kind, c.kind === "repo" ? c.from || "link" : "")}
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
  uxName,
  onClick,
}: {
  candidate: SolutionCandidate;
  busy: boolean;
  disabled: boolean;
  cta: string;
  badge: string;
  archivedLabel: string;
  uxName: string;
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
        data-ux={uxName}
        className="inline-flex shrink-0 items-center justify-center gap-1.5 rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm font-medium text-gray-900 dark:text-gray-100 transition-colors hover:bg-gray-50 dark:hover:bg-gray-800 disabled:cursor-not-allowed disabled:opacity-60"
      >
        {busy && <Spinner size="sm" />}
        {cta}
      </button>
    </li>
  );
}

/** GitHub's own mark: the field under it searches GitHub, and the logo says so without a word of copy. */
function GithubMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden className={className}>
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0016 8c0-4.42-3.58-8-8-8z" />
    </svg>
  );
}

/**
 * One catalog project as a logo chip.
 *
 * The showroom is not the offer. Hero cards with a full-width blue CTA each
 * shouted louder than the Git path above them; a row apiece with name, tagline,
 * repository and licence merely shouted quieter while still spending four lines
 * on four demo apps. The logo is the recognisable part, so the chip carries the
 * mark and the name, the whole chip is the button, and the tagline retreats to
 * the tooltip.
 */
function SolutionChip({
  solution,
  busy,
  disabled,
  uxName,
  onClick,
}: {
  solution: Solution;
  busy: boolean;
  disabled: boolean;
  uxName: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      data-ux={uxName}
      title={solution.tagline}
      className="inline-flex items-center gap-2 rounded-full border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 py-1.5 pl-1.5 pr-3.5 text-sm font-medium text-gray-900 dark:text-gray-100 transition-colors hover:border-blue-400 hover:bg-blue-50 dark:hover:bg-blue-950/30 disabled:cursor-not-allowed disabled:opacity-60"
    >
      {busy ? (
        <span className="flex h-6 w-6 items-center justify-center">
          <Spinner size="sm" />
        </span>
      ) : solution.icon ? (
        <img
          src={solution.icon}
          alt=""
          className="h-6 w-6 rounded-full bg-gray-100 dark:bg-gray-800 object-cover"
        />
      ) : (
        <span className="flex h-6 w-6 items-center justify-center rounded-full bg-blue-100 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400">
          <ResourceIcon name="apps" className="h-3.5 w-3.5" />
        </span>
      )}
      {solution.name}
    </button>
  );
}

function SolutionChipSkeleton() {
  return <div className="h-9 w-32 animate-pulse rounded-full bg-gray-100 dark:bg-gray-800" />;
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

/**
 * One catalog card.
 *
 * The source badge is not decoration: a repo entry costs a build of several
 * minutes and an image entry starts in seconds with a disk attached, and the
 * customer deserves to know which of the two they clicked before they wait.
 */
function SolutionCard({
  solution,
  cta,
  busy,
  disabled,
  uxName,
  onClick,
  t,
}: {
  solution: Solution;
  cta: string;
  busy: boolean;
  disabled: boolean;
  uxName: string;
  onClick: () => void;
  t: Translate;
}) {
  const image = solution.source === "image";
  return (
    <div className="flex flex-col rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
      <div className="mb-4 flex h-10 w-10 items-center justify-center rounded-lg bg-blue-100 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400">
        <ResourceIcon name="apps" className="h-5 w-5" />
      </div>
      <p className="text-sm font-semibold text-gray-900 dark:text-gray-100">{solution.name}</p>
      <p className="mt-1 flex-1 text-sm text-gray-500 dark:text-gray-400">{solution.tagline}</p>
      <div className="mt-3 flex flex-wrap items-center gap-1.5">
        <span
          title={t(image ? "overview.templates.source.imageHint" : "overview.templates.source.repoHint")}
          className={`inline-flex items-center rounded-full px-2 py-0.5 text-[11px] font-medium ${
            image
              ? "bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300"
              : "bg-blue-50 text-blue-700 dark:bg-blue-950/40 dark:text-blue-300"
          }`}
        >
          {t(image ? "overview.templates.source.image" : "overview.templates.source.repo")}
        </span>
        {solution.volume?.size && (
          <span className="inline-flex items-center rounded-full bg-gray-100 px-2 py-0.5 text-[11px] font-medium text-gray-600 dark:bg-gray-800 dark:text-gray-300">
            {t("overview.templates.volume", { size: solution.volume.size })}
          </span>
        )}
      </div>
      <p
        className="mt-2 truncate text-xs text-gray-400 dark:text-gray-500"
        title={image ? solution.image : solution.repo}
      >
        {(image ? solution.image : solution.repo) || solution.name} · {solution.license}
      </p>
      <button
        type="button"
        onClick={onClick}
        disabled={disabled}
        data-ux={uxName}
        className="mt-4 inline-flex items-center justify-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-60"
      >
        {busy && <Spinner size="sm" />}
        {cta}
      </button>
    </div>
  );
}

/**
 * Collects the values an entry declares it needs before the install call.
 *
 * Required fields are enforced here rather than server-side alone because the
 * failure they prevent is not a validation error but a running app on a public
 * address with no password on it.
 */
function SolutionParamsDialog({
  solution,
  busy,
  t,
  onCancel,
  onSubmit,
}: {
  solution: Solution;
  busy: boolean;
  t: Translate;
  onCancel: () => void;
  onSubmit: (values: Record<string, string>) => void;
}) {
  const params = solution.params ?? [];
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(params.map((p) => [p.key, p.default || (p.kind === "select" ? (p.options ?? [])[0] ?? "" : "")])),
  );
  const [error, setError] = useState<string | null>(null);

  function submit() {
    const missing = params.some((p) => p.required && !(values[p.key] ?? "").trim());
    if (missing) {
      setError(t("overview.templates.params.missing"));
      return;
    }
    onSubmit(values);
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <div className="w-full max-w-md rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-xl">
        <p className="text-sm font-semibold text-gray-900 dark:text-gray-100">
          {solution.name} · {t("overview.templates.params.title")}
        </p>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("overview.templates.params.hint")}</p>
        <div className="mt-4 space-y-3">
          {params.map((p) => (
            <label key={p.key} className="block">
              <span className="text-xs font-medium text-gray-700 dark:text-gray-300">
                {p.label || p.key}
                {p.required && (
                  <span className="ml-1 text-gray-400">({t("overview.templates.params.required")})</span>
                )}
              </span>
              {p.kind === "select" ? (
                <select
                  value={values[p.key] ?? ""}
                  onChange={(e) => setValues((v) => ({ ...v, [p.key]: e.target.value }))}
                  className="mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 focus:border-blue-500 focus:outline-none"
                >
                  {(p.options ?? []).map((o) => (
                    <option key={o} value={o}>
                      {o}
                    </option>
                  ))}
                </select>
              ) : (
                <input
                  type={p.kind === "secret" ? "password" : "text"}
                  value={values[p.key] ?? ""}
                  placeholder={p.placeholder}
                  onChange={(e) => setValues((v) => ({ ...v, [p.key]: e.target.value }))}
                  className="mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 focus:border-blue-500 focus:outline-none"
                />
              )}
              {p.help && <span className="mt-1 block text-xs text-gray-400 dark:text-gray-500">{p.help}</span>}
            </label>
          ))}
        </div>
        {error && <p className="mt-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
        <div className="mt-5 flex justify-end gap-2">
          <button
            type="button"
            onClick={onCancel}
            className="rounded-lg border border-gray-300 dark:border-gray-700 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-200"
          >
            {t("overview.templates.params.cancel")}
          </button>
          <button
            type="button"
            onClick={submit}
            disabled={busy}
            className="inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-60"
          >
            {busy && <Spinner size="sm" />}
            {t("overview.templates.params.submit")}
          </button>
        </div>
      </div>
    </div>
  );
}

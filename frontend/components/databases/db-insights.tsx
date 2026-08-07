"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { databasesApi } from "@/lib/api";
import type {
  DatabaseAdvisory,
  DatabaseInsights,
  DatabaseQueryStat,
  DatabaseTableCard,
} from "@/lib/types";
import { formatBytes } from "@/components/charts/format";
import { Spinner } from "@/components/ui/spinner";
import { CopyButton } from "@/components/ui/copy-button";
import { timeAgo } from "@/lib/format";
import { useT } from "@/lib/i18n/console/context";

function pct(v: number): string {
  return `${(v * 100).toFixed(v >= 0.995 ? 1 : 0)}%`;
}

function ms(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(1)} s`;
  return `${Math.round(v)} ms`;
}

function count(v: number): string {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
  if (v >= 1_000) return `${(v / 1_000).toFixed(0)}k`;
  return String(v);
}

const severityDots: Record<string, string> = {
  critical: "bg-red-500",
  warning: "bg-amber-500",
  info: "bg-gray-400",
};

const severityRank: Record<string, number> = { critical: 2, warning: 1, info: 0 };

const codePriority: Record<string, number> = {
  quota_forecast: 9,
  slow_query: 8,
  low_cache_hit: 7,
  idle_in_transaction: 6,
  append_only_no_retention: 5,
  unused_index: 4,
  stale_stats: 1,
};

function evNum(a: DatabaseAdvisory, key: string): number | null {
  const v = a.evidence?.[key];
  return typeof v === "number" && Number.isFinite(v) ? v : null;
}

function evStr(a: DatabaseAdvisory, key: string): string {
  const v = a.evidence?.[key];
  return typeof v === "string" ? v : "";
}

/**
 * humanBody turns one advisory into a plain-language sentence built from its
 * evidence numbers. The backend's machine detail ("mean=84480ms, calls=14")
 * stays available behind the expand, but nobody should have to read it to
 * understand what is wrong. Falls back to the raw detail when evidence is
 * missing the expected keys.
 */
function humanBody(a: DatabaseAdvisory, t: (k: string, v?: Record<string, string | number>) => string, locale: string): string {
  switch (a.code) {
    case "slow_query": {
      const share = evNum(a, "share");
      const mean = evNum(a, "meanMs");
      const calls = evNum(a, "calls");
      if (share !== null && mean !== null && calls !== null) {
        return t("databases.insights.human.slow_query", {
          share: pct(share),
          mean: ms(mean),
          calls: count(calls),
        });
      }
      break;
    }
    case "low_cache_hit": {
      const ratio = evNum(a, "hitRatio");
      const read = evNum(a, "bytesRead");
      if (ratio !== null && read !== null) {
        return t("databases.insights.human.low_cache_hit", {
          table: a.subject,
          pct: pct(ratio),
          read: formatBytes(read),
        });
      }
      break;
    }
    case "append_only_no_retention": {
      const weekly = evNum(a, "growthBytesPerWeek");
      if (weekly !== null) {
        return t("databases.insights.human.append_only", {
          table: a.subject,
          growth: formatBytes(weekly),
        });
      }
      break;
    }
    case "unused_index": {
      const size = evNum(a, "sizeBytes");
      const hours = evNum(a, "windowHours");
      if (size !== null && hours !== null) {
        return t("databases.insights.human.unused_index", {
          index: a.subject,
          size: formatBytes(size),
          days: String(Math.max(1, Math.round(hours / 24))),
        });
      }
      break;
    }
    case "stale_stats":
      return t("databases.insights.human.stale_stats", { table: a.subject });
    case "quota_forecast": {
      const perDay = evNum(a, "growthBytesPerDay");
      const at = evStr(a, "exhaustedAt");
      if (perDay !== null && at) {
        return t("databases.insights.human.quota_forecast", {
          perDay: formatBytes(perDay),
          date: shortDate(at, locale),
        });
      }
      break;
    }
    case "idle_in_transaction": {
      const n = evNum(a, "connections");
      const open = evNum(a, "openSeconds");
      if (n !== null && open !== null) {
        return t("databases.insights.human.idle_in_transaction", {
          n: String(n),
          dur: open >= 3600 ? `${Math.round(open / 3600)} h` : `${Math.round(open / 60)} min`,
        });
      }
      break;
    }
  }
  return a.detail;
}

function shortDate(iso: string, locale: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return new Intl.DateTimeFormat(locale === "ru" ? "ru-RU" : "en-US", {
    day: "numeric",
    month: "short",
  })
    .format(d)
    .replace(/\.$/, "");
}

function advisoryScore(a: DatabaseAdvisory): number {
  const impact = evNum(a, "share") ?? (evNum(a, "sizeBytes") ?? 0) / 1e12;
  return (severityRank[a.severity] ?? 0) * 1000 + (codePriority[a.code] ?? 0) * 10 + Math.min(impact, 9);
}

/**
 * AdvisoryCard is the expanded, single-finding view: plain-language sentence
 * first, machine detail and the query sample behind it, suggested SQL with an
 * explicit "you run it, not us" note. Also used on the table page.
 */
export function AdvisoryCard({ a }: { a: DatabaseAdvisory }) {
  const { t, locale } = useT();
  const title = t(`databases.insights.code.${a.code}`);
  const sample = evStr(a, "querySample");
  return (
    <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4 shadow-sm">
      <div className="flex flex-wrap items-center gap-2">
        <span className={`h-2 w-2 rounded-full ${severityDots[a.severity] ?? severityDots.info}`} />
        <span className="text-sm font-semibold text-gray-900 dark:text-gray-100">{title}</span>
        <span className="ml-auto text-xs text-gray-400 dark:text-gray-500">
          {t("databases.insights.advisories.since", { ago: timeAgo(a.firstSeenAt) })}
        </span>
      </div>
      <p className="mt-2 text-sm text-gray-700 dark:text-gray-300">{humanBody(a, t, locale)}</p>
      {sample && (
        <pre className="mt-2 max-h-40 overflow-auto rounded-md border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-950 px-3 py-2 font-mono text-xs text-gray-700 dark:text-gray-300">
          {sample}
        </pre>
      )}
      {a.suggestedSql && (
        <div className="mt-3">
          <div className="flex items-center gap-2">
            <code className="flex-1 break-all rounded-md border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-950 px-3 py-2 font-mono text-xs text-gray-800 dark:text-gray-200">
              {a.suggestedSql}
            </code>
            <CopyButton value={a.suggestedSql} />
          </div>
          <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("databases.insights.advisories.sqlHint")}</p>
        </div>
      )}
    </div>
  );
}

function HeroTile({
  label,
  value,
  sub,
  subTone = "muted",
  bar,
}: {
  label: string;
  value: string;
  sub?: string;
  subTone?: "muted" | "good" | "warn" | "bad";
  bar?: number;
}) {
  const subColors = {
    muted: "text-gray-500 dark:text-gray-400",
    good: "text-emerald-600 dark:text-emerald-400",
    warn: "text-amber-600 dark:text-amber-400",
    bad: "text-red-600 dark:text-red-400",
  };
  return (
    <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4 shadow-sm">
      <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{label}</p>
      <p className="mt-1 text-2xl font-semibold tracking-tight text-gray-900 dark:text-gray-100">{value}</p>
      {typeof bar === "number" && (
        <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
          <div
            className={`h-full rounded-full ${bar > 0.85 ? "bg-red-500" : bar > 0.65 ? "bg-amber-500" : "bg-blue-500"}`}
            style={{ width: `${Math.min(100, Math.round(bar * 100))}%` }}
          />
        </div>
      )}
      {sub && <p className={`mt-1 text-xs ${subColors[subTone]}`}>{sub}</p>}
    </div>
  );
}

/**
 * DiagnosisPanel is the first screen: one finding, chosen as the most severe
 * and most impactful, told as a paragraph a person can act on. Everything
 * else is a count ("ещё 12 находок") that expands into the grouped list.
 */
function DiagnosisPanel({ advisories }: { advisories: DatabaseAdvisory[] }) {
  const { t, locale } = useT();
  const [expanded, setExpanded] = useState(false);
  const sorted = useMemo(() => [...advisories].sort((x, y) => advisoryScore(y) - advisoryScore(x)), [advisories]);
  const top = sorted[0];
  if (!top) {
    return (
      <div className="rounded-xl border border-emerald-200 dark:border-emerald-900 bg-emerald-50 dark:bg-emerald-950/30 px-5 py-4">
        <p className="text-sm font-medium text-emerald-800 dark:text-emerald-300">
          {t("databases.insights.diagnosis.healthy")}
        </p>
      </div>
    );
  }
  const rest = sorted.slice(1);
  const sample = evStr(top, "querySample");
  return (
    <div className="rounded-xl border border-indigo-200 dark:border-indigo-900/60 bg-indigo-50/60 dark:bg-indigo-950/20 p-5">
      <div className="flex flex-wrap items-center gap-2">
        <span className={`h-2 w-2 rounded-full ${severityDots[top.severity] ?? severityDots.info}`} />
        <span className="text-xs font-semibold uppercase tracking-wide text-indigo-700 dark:text-indigo-400">
          {t("databases.insights.diagnosis.title")}
        </span>
        <span className="ml-auto text-xs text-gray-400 dark:text-gray-500">
          {t("databases.insights.advisories.since", { ago: timeAgo(top.firstSeenAt) })}
        </span>
      </div>
      <p className="mt-2 text-base leading-relaxed text-gray-900 dark:text-gray-100">
        {humanBody(top, t, locale)}
      </p>
      {sample && (
        <pre className="mt-3 max-h-32 overflow-auto rounded-lg border border-indigo-100 dark:border-indigo-900/40 bg-white dark:bg-gray-950 px-3 py-2 font-mono text-xs text-gray-600 dark:text-gray-400">
          {sample}
        </pre>
      )}
      {top.suggestedSql && (
        <div className="mt-3 flex items-center gap-2">
          <code className="flex-1 break-all rounded-md border border-indigo-100 dark:border-indigo-900/40 bg-white dark:bg-gray-950 px-3 py-2 font-mono text-xs text-gray-800 dark:text-gray-200">
            {top.suggestedSql}
          </code>
          <CopyButton value={top.suggestedSql} />
        </div>
      )}
      {top.suggestedSql && (
        <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{t("databases.insights.advisories.sqlHint")}</p>
      )}
      {rest.length > 0 && (
        <div className="mt-4 border-t border-indigo-100 dark:border-indigo-900/40 pt-3">
          <button
            type="button"
            onClick={() => setExpanded((v) => !v)}
            className="text-sm font-medium text-indigo-700 hover:text-indigo-900 dark:text-indigo-400 dark:hover:text-indigo-300"
          >
            {expanded
              ? t("databases.insights.diagnosis.hideRest")
              : t("databases.insights.diagnosis.showRest", { n: String(rest.length) })}
          </button>
          {expanded && (
            <div className="mt-3 space-y-3">
              {rest.map((a) => (
                <AdvisoryCard key={`${a.code}:${a.subject}`} a={a} />
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function TableCard({ tbl, href }: { tbl: DatabaseTableCard; href: string }) {
  const { t } = useT();
  const badVitals =
    (typeof tbl.cacheHitRatio === "number" && tbl.cacheHitRatio < 0.9) || tbl.appendOnly;
  return (
    <Link
      href={href}
      className="block rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4 shadow-sm transition hover:border-gray-300 dark:hover:border-gray-700 hover:shadow-md"
    >
      <div className="flex items-baseline justify-between gap-2">
        <span className="truncate font-mono text-sm font-medium text-gray-900 dark:text-gray-100">{tbl.name}</span>
        <span className="shrink-0 text-sm font-semibold text-gray-900 dark:text-gray-100">{formatBytes(tbl.totalBytes)}</span>
      </div>
      <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
        {t("databases.insights.tables.rows", { rows: count(tbl.rowsEstimate) })}
        {typeof tbl.growthBytes === "number" && tbl.growthBytes > 0 && (
          <> · {t("databases.insights.tables.growth", { size: formatBytes(tbl.growthBytes), hours: String(tbl.windowHours) })}</>
        )}
      </p>
      {badVitals && (
        <div className="mt-2 flex flex-wrap gap-1.5">
          {tbl.appendOnly && (
            <span className="rounded-md bg-amber-100 dark:bg-amber-950/50 px-2 py-0.5 text-xs text-amber-700 dark:text-amber-400">
              {t("databases.insights.tables.appendOnly")}
            </span>
          )}
          {typeof tbl.cacheHitRatio === "number" && tbl.cacheHitRatio < 0.9 && (
            <span className="rounded-md bg-red-100 dark:bg-red-950/50 px-2 py-0.5 text-xs text-red-700 dark:text-red-400">
              {t("databases.insights.tables.cacheHit", { pct: pct(tbl.cacheHitRatio) })}
            </span>
          )}
        </div>
      )}
    </Link>
  );
}

function QueryRow({ q }: { q: DatabaseQueryStat }) {
  const { t } = useT();
  const [open, setOpen] = useState(false);
  const oneLine = q.query.replace(/\s+/g, " ").trim();
  return (
    <div className="border-t border-gray-100 dark:border-gray-800 py-3 first:border-t-0">
      <p className="text-sm text-gray-900 dark:text-gray-100">
        <span className="font-semibold">{ms(q.meanMs)}</span>{" "}
        <span className="text-gray-500 dark:text-gray-400">
          {t("databases.insights.queries.perCall")} · {t("databases.insights.queries.calls", { calls: count(q.calls) })} ·{" "}
          {t("databases.insights.queries.share", { pct: pct(q.share) })}
        </span>
      </p>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="mt-1 block w-full text-left"
      >
        {open ? (
          <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-md bg-gray-50 dark:bg-gray-950 px-2 py-1.5 font-mono text-xs text-gray-700 dark:text-gray-300">
            {q.query}
          </pre>
        ) : (
          <span className="block truncate font-mono text-xs text-gray-400 dark:text-gray-500">{oneLine}</span>
        )}
      </button>
    </div>
  );
}

/**
 * DbInsights renders what the platform observed inside one managed database.
 * Reading order is deliberate: four numbers, then ONE diagnosis paragraph,
 * then evidence (tables, queries) for those who want to dig. Raw SQL never
 * appears until the user asks for it.
 *
 * Each secondary panel swallows its own failure, so a database whose advisory
 * engine has not run yet still shows its size and growth. A database with no
 * samples at all is the normal state for the first hour of its life and is
 * explained rather than reported as an error.
 */
export function DbInsights({ projectId, envId, name }: { projectId: string; envId: string; name: string }) {
  const { t } = useT();
  const [insights, setInsights] = useState<DatabaseInsights | null>(null);
  const [advisories, setAdvisories] = useState<DatabaseAdvisory[]>([]);
  const [tables, setTables] = useState<DatabaseTableCard[]>([]);
  const [queries, setQueries] = useState<DatabaseQueryStat[]>([]);
  const [now, setNow] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!envId) return;
    let cancelled = false;
    Promise.all([
      databasesApi.insights(projectId, envId, name),
      databasesApi.advisories(projectId, envId, name).catch(() => ({ advisories: [] })),
      databasesApi.tables(projectId, envId, name).catch(() => ({ tables: [] })),
      databasesApi.queries(projectId, envId, name).catch(() => ({ queries: [], totalMs: 0 })),
    ])
      .then(([i, a, tb, q]) => {
        if (cancelled) return;
        setInsights(i);
        setAdvisories(a.advisories ?? []);
        setTables(tb.tables ?? []);
        setQueries(q.queries ?? []);
        setNow(Date.now());
        setError(null);
      })
      .catch((e) => !cancelled && setError(e instanceof Error ? e.message : t("databases.insights.error")))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [projectId, envId, name, t]);

  if (loading) {
    return (
      <section className="mb-8">
        <div className="flex h-24 items-center justify-center rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900">
          <Spinner size="md" />
        </div>
      </section>
    );
  }
  if (error) {
    return (
      <section className="mb-8">
        <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
          {error}
        </div>
      </section>
    );
  }
  if (!insights || !insights.collectedAt) {
    return (
      <section className="mb-8">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
          {t("databases.insights.title")}
        </h2>
        <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm">
          <p className="text-sm font-medium text-gray-900 dark:text-gray-100">{t("databases.insights.pending.title")}</p>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("databases.insights.pending.body")}</p>
        </div>
      </section>
    );
  }

  return (
    <DbInsightsView
      projectId={projectId}
      name={name}
      insights={insights}
      advisories={advisories}
      tables={tables}
      queries={queries}
      now={now}
    />
  );
}

/**
 * DbInsightsView is the pure presentation of the tab: it takes already-loaded
 * data and renders the tiles, diagnosis, tables and queries. Kept separate
 * from the fetching wrapper so it can be rendered against fixtures.
 */
export function DbInsightsView({
  projectId,
  name,
  insights,
  advisories,
  tables,
  queries,
  now,
}: {
  projectId: string;
  name: string;
  insights: DatabaseInsights;
  advisories: DatabaseAdvisory[];
  tables: DatabaseTableCard[];
  queries: DatabaseQueryStat[];
  now: number;
}) {
  const { t, locale } = useT();
  const [allTables, setAllTables] = useState(false);
  const [allQueries, setAllQueries] = useState(false);

  const limit = insights.sizeLimitBytes ?? 0;
  const size = insights.sizeBytes ?? 0;
  const growth = insights.growthBytes7d ?? 0;
  const forecast = advisories.find((a) => a.code === "quota_forecast");
  const forecastAt = forecast ? evStr(forecast, "exhaustedAt") : "";
  const computedDaysLeft = limit > 0 && growth > 0 ? ((limit - size) / (growth / 7)) : Infinity;
  const runsOutDate = forecastAt
    ? shortDate(forecastAt, locale)
    : Number.isFinite(computedDaysLeft) && computedDaysLeft < 90
      ? shortDate(new Date(now + computedDaysLeft * 86_400_000).toISOString(), locale)
      : "";
  const daysLeft = forecast ? (evNum(forecast, "daysToLimit") ?? computedDaysLeft) : computedDaysLeft;
  const visibleTables = allTables ? tables : tables.slice(0, 6);
  const visibleQueries = allQueries ? queries : queries.slice(0, 5);

  return (
    <section className="mb-8">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
          {t("databases.insights.title")}
        </h2>
        <span className={`text-xs ${insights.stale ? "text-amber-600 dark:text-amber-500" : "text-gray-400 dark:text-gray-500"}`}>
          {insights.stale
            ? t("databases.insights.stale", { ago: timeAgo(insights.collectedAt ?? "") })
            : t("databases.insights.collected", { ago: timeAgo(insights.collectedAt ?? "") })}
        </span>
      </div>

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <HeroTile
          label={t("databases.insights.hero.quota")}
          value={limit > 0 ? `${formatBytes(size)} / ${formatBytes(limit)}` : formatBytes(size)}
          bar={limit > 0 ? size / limit : undefined}
          sub={
            growth !== 0
              ? t(growth > 0 ? "databases.insights.hero.growth7d" : "databases.insights.hero.shrunk7d", {
                  size: formatBytes(Math.abs(growth)),
                })
              : undefined
          }
          subTone={growth > 0 && limit > 0 && daysLeft < 30 ? "warn" : "muted"}
        />
        <HeroTile
          label={t("databases.insights.stat.cacheHit")}
          value={typeof insights.cacheHitRatio === "number" ? pct(insights.cacheHitRatio) : "—"}
          sub={
            typeof insights.cacheHitRatio === "number"
              ? insights.cacheHitRatio < 0.9
                ? t("databases.insights.hero.cacheLow")
                : t("databases.insights.hero.ok")
              : undefined
          }
          subTone={typeof insights.cacheHitRatio === "number" && insights.cacheHitRatio < 0.9 ? "bad" : "good"}
        />
        <HeroTile
          label={t("databases.insights.stat.connections")}
          value={String(insights.connections ?? 0)}
          sub={t("databases.insights.hero.ok")}
          subTone="good"
        />
        {runsOutDate && (
          <HeroTile
            label={t("databases.insights.hero.runsOut")}
            value={`≈ ${runsOutDate}`}
            sub={t("databases.insights.hero.atCurrentGrowth")}
            subTone={daysLeft < 14 ? "bad" : "warn"}
          />
        )}
      </div>

      <div className="mt-4">
        <DiagnosisPanel advisories={advisories} />
      </div>

      {tables.length > 0 && (
        <div className="mt-6">
          <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
            {allTables || tables.length <= 6
              ? t("databases.insights.tables.title")
              : t("databases.insights.tables.topOf", { top: "6", total: String(tables.length) })}
          </h3>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {visibleTables.map((tbl) => (
              <TableCard
                key={`${tbl.schema}.${tbl.name}`}
                tbl={tbl}
                href={`/projects/${projectId}/databases/${name}/tables/${encodeURIComponent(tbl.name)}${tbl.schema && tbl.schema !== "public" ? `?schema=${encodeURIComponent(tbl.schema)}` : ""}`}
              />
            ))}
          </div>
          {tables.length > 6 && (
            <button
              type="button"
              onClick={() => setAllTables((v) => !v)}
              className="mt-2 text-sm font-medium text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200"
            >
              {allTables
                ? t("databases.insights.showLess")
                : t("databases.insights.tables.showAll", { n: String(tables.length) })}
            </button>
          )}
        </div>
      )}

      {queries.length > 0 && (
        <div className="mt-6">
          <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
            {t("databases.insights.queries.title")}
          </h3>
          <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-4 py-2 shadow-sm">
            {visibleQueries.map((q) => (
              <QueryRow key={q.queryId} q={q} />
            ))}
            <div className="flex items-center justify-between border-t border-gray-100 dark:border-gray-800 py-2">
              <p className="text-xs text-gray-400 dark:text-gray-500">{t("databases.insights.queries.hint")}</p>
              {queries.length > 5 && (
                <button
                  type="button"
                  onClick={() => setAllQueries((v) => !v)}
                  className="text-sm font-medium text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200"
                >
                  {allQueries
                    ? t("databases.insights.showLess")
                    : t("databases.insights.queries.showAll", { n: String(queries.length) })}
                </button>
              )}
            </div>
          </div>
        </div>
      )}
    </section>
  );
}

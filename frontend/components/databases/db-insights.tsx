"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
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

const severityStyles: Record<string, string> = {
  critical: "border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/30",
  warning: "border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/30",
  info: "border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900",
};

const severityDots: Record<string, string> = {
  critical: "bg-red-500",
  warning: "bg-amber-500",
  info: "bg-gray-400",
};

function Stat({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div>
      <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{label}</p>
      <p className="mt-1 text-lg font-semibold text-gray-900 dark:text-gray-100">{value}</p>
      {sub && <p className="text-xs text-gray-500 dark:text-gray-400">{sub}</p>}
    </div>
  );
}

export function AdvisoryCard({ a }: { a: DatabaseAdvisory }) {
  const { t } = useT();
  const title = t(`databases.insights.code.${a.code}`);
  const sample = typeof a.evidence?.querySample === "string" ? (a.evidence.querySample as string) : "";
  return (
    <div className={`rounded-xl border p-4 shadow-sm ${severityStyles[a.severity] ?? severityStyles.info}`}>
      <div className="flex flex-wrap items-center gap-2">
        <span className={`h-2 w-2 rounded-full ${severityDots[a.severity] ?? severityDots.info}`} />
        <span className="text-sm font-semibold text-gray-900 dark:text-gray-100">{title}</span>
        <span className="font-mono text-xs text-gray-500 dark:text-gray-400">{a.subject}</span>
        <span className="ml-auto text-xs text-gray-400 dark:text-gray-500">
          {t("databases.insights.advisories.since", { ago: timeAgo(a.firstSeenAt) })}
        </span>
      </div>
      <p className="mt-2 font-mono text-xs text-gray-600 dark:text-gray-300">{a.detail}</p>
      {sample && (
        <pre className="mt-2 overflow-x-auto rounded-md border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-950 px-3 py-2 font-mono text-xs text-gray-700 dark:text-gray-300">
          {sample}
        </pre>
      )}
      {a.suggestedSql && (
        <div className="mt-3">
          <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("databases.insights.advisories.sql")}</p>
          <div className="mt-1 flex items-center gap-2">
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

function TableCard({ tbl, href }: { tbl: DatabaseTableCard; href: string }) {
  const { t } = useT();
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
        {t("databases.insights.tables.rows", { rows: count(tbl.rowsEstimate) })} ·{" "}
        {t("databases.insights.tables.heap", { size: formatBytes(tbl.heapBytes) })} ·{" "}
        {t("databases.insights.tables.indexes", { size: formatBytes(tbl.indexBytes) })}
      </p>
      <div className="mt-2 flex flex-wrap gap-1.5">
        {typeof tbl.growthBytes === "number" && tbl.growthBytes > 0 && (
          <span className="rounded-md bg-gray-100 dark:bg-gray-800 px-2 py-0.5 text-xs text-gray-600 dark:text-gray-300">
            {t("databases.insights.tables.growth", {
              size: formatBytes(tbl.growthBytes),
              hours: String(tbl.windowHours),
            })}
          </span>
        )}
        {tbl.appendOnly && (
          <span className="rounded-md bg-amber-100 dark:bg-amber-950/50 px-2 py-0.5 text-xs text-amber-700 dark:text-amber-400">
            {t("databases.insights.tables.appendOnly")}
          </span>
        )}
        {typeof tbl.cacheHitRatio === "number" && (
          <span
            className={`rounded-md px-2 py-0.5 text-xs ${
              tbl.cacheHitRatio < 0.9
                ? "bg-red-100 dark:bg-red-950/50 text-red-700 dark:text-red-400"
                : "bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-300"
            }`}
          >
            {t("databases.insights.tables.cacheHit", { pct: pct(tbl.cacheHitRatio) })}
          </span>
        )}
        <span className="rounded-md bg-gray-100 dark:bg-gray-800 px-2 py-0.5 text-xs text-gray-500 dark:text-gray-400">
          {tbl.lastAutoanalyze
            ? t("databases.insights.tables.analyzed", { ago: timeAgo(tbl.lastAutoanalyze) })
            : t("databases.insights.tables.neverAnalyzed")}
        </span>
      </div>
    </Link>
  );
}

function QueryRow({ q }: { q: DatabaseQueryStat }) {
  const { t } = useT();
  return (
    <div className="border-t border-gray-100 dark:border-gray-800 py-3 first:border-t-0">
      <pre className="overflow-x-auto whitespace-pre-wrap break-all font-mono text-xs text-gray-700 dark:text-gray-300">
        {q.query}
      </pre>
      <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
        {t("databases.insights.queries.mean", { ms: ms(q.meanMs) })} ·{" "}
        {t("databases.insights.queries.calls", { calls: count(q.calls) })} ·{" "}
        {t("databases.insights.queries.share", { pct: pct(q.share) })}
      </p>
    </div>
  );
}

/**
 * DbInsights renders what the platform observed inside one managed database:
 * headline numbers, the findings the advisory engine derived, table cards and
 * the heaviest queries.
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

  const limit = insights.sizeLimitBytes ?? 0;
  const size = insights.sizeBytes ?? 0;
  const growth = insights.growthBytes7d ?? 0;

  return (
    <section className="mb-8">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
          {t("databases.insights.title")}
        </h2>
        <span className={`text-xs ${insights.stale ? "text-amber-600 dark:text-amber-500" : "text-gray-400 dark:text-gray-500"}`}>
          {insights.stale
            ? t("databases.insights.stale", { ago: timeAgo(insights.collectedAt) })
            : t("databases.insights.collected", { ago: timeAgo(insights.collectedAt) })}
        </span>
      </div>

      <div className="grid gap-4 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm sm:grid-cols-2 lg:grid-cols-4">
        <Stat
          label={t("databases.insights.stat.size")}
          value={formatBytes(size)}
          sub={limit > 0 ? t("databases.insights.stat.ofLimit", { limit: formatBytes(limit) }) : undefined}
        />
        <Stat
          label={t("databases.insights.stat.growth")}
          value={`${growth >= 0 ? "+" : "-"}${formatBytes(Math.abs(growth))}`}
        />
        <Stat
          label={t("databases.insights.stat.cacheHit")}
          value={typeof insights.cacheHitRatio === "number" ? pct(insights.cacheHitRatio) : "—"}
        />
        <Stat label={t("databases.insights.stat.connections")} value={String(insights.connections ?? 0)} />
      </div>

      <div className="mt-6">
        <h3 className="mb-2 text-sm font-semibold text-gray-700 dark:text-gray-300">
          {t("databases.insights.advisories.title")}
        </h3>
        {advisories.length === 0 ? (
          <p className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-4 py-3 text-sm text-gray-500 dark:text-gray-400">
            {t("databases.insights.advisories.empty")}
          </p>
        ) : (
          <div className="space-y-3">
            {advisories.map((a) => (
              <AdvisoryCard key={`${a.code}:${a.subject}`} a={a} />
            ))}
          </div>
        )}
      </div>

      <div className="mt-6">
        <h3 className="mb-2 text-sm font-semibold text-gray-700 dark:text-gray-300">
          {t("databases.insights.tables.title")}
        </h3>
        {tables.length === 0 ? (
          <p className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-4 py-3 text-sm text-gray-500 dark:text-gray-400">
            {t("databases.insights.tables.empty")}
          </p>
        ) : (
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {tables.map((tbl) => (
              <TableCard
                key={`${tbl.schema}.${tbl.name}`}
                tbl={tbl}
                href={`/projects/${projectId}/databases/${name}/tables/${encodeURIComponent(tbl.name)}${tbl.schema && tbl.schema !== "public" ? `?schema=${encodeURIComponent(tbl.schema)}` : ""}`}
              />
            ))}
          </div>
        )}
      </div>

      <div className="mt-6">
        <h3 className="mb-2 text-sm font-semibold text-gray-700 dark:text-gray-300">
          {t("databases.insights.queries.title")}
        </h3>
        <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-4 py-2 shadow-sm">
          {queries.length === 0 ? (
            <p className="py-2 text-sm text-gray-500 dark:text-gray-400">{t("databases.insights.queries.empty")}</p>
          ) : (
            <>
              {queries.map((q) => (
                <QueryRow key={q.queryId} q={q} />
              ))}
              <p className="border-t border-gray-100 dark:border-gray-800 py-2 text-xs text-gray-400 dark:text-gray-500">
                {t("databases.insights.queries.hint")}
              </p>
            </>
          )}
        </div>
      </div>
    </section>
  );
}

"use client";

import { useEffect, useState } from "react";
import { useParams, useSearchParams } from "next/navigation";
import { databasesApi } from "@/lib/api";
import type { DatabaseTableDetailResponse, DatabaseTableIndex, DatabaseTableQuery, DatabaseTableSeriesPoint } from "@/lib/types";
import { formatBytes } from "@/components/charts/format";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { AdvisoryCard } from "@/components/databases/db-insights";
import { useProjectContext } from "@/lib/project-context";
import { timeAgo } from "@/lib/format";
import { useT } from "@/lib/i18n/console/context";

function count(v: number): string {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
  if (v >= 1_000) return `${(v / 1_000).toFixed(0)}k`;
  return String(v);
}

function ms(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(1)} s`;
  return `${Math.round(v)} ms`;
}

function pct(v: number): string {
  return `${(v * 100).toFixed(v >= 0.995 ? 1 : 0)}%`;
}

function Stat({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div>
      <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{label}</p>
      <p className="mt-1 text-lg font-semibold text-gray-900 dark:text-gray-100">{value}</p>
      {sub && <p className="text-xs text-gray-500 dark:text-gray-400">{sub}</p>}
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="mt-8">
      <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{title}</h2>
      <div className="mt-3">{children}</div>
    </section>
  );
}

/** Size history drawn as an inline area chart; a single sample renders as a flat line. */
function SizeSparkline({ points }: { points: DatabaseTableSeriesPoint[] }) {
  const { t } = useT();
  if (points.length < 2) return null;
  const w = 640;
  const h = 120;
  const values = points.map((p) => p.totalBytes);
  const min = Math.min(...values);
  const max = Math.max(...values);
  const span = max - min || 1;
  const x = (i: number) => (i / (points.length - 1)) * w;
  const y = (v: number) => h - ((v - min) / span) * (h - 12) - 6;
  const line = points.map((p, i) => `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(p.totalBytes).toFixed(1)}`).join(" ");
  const area = `${line} L${w},${h} L0,${h} Z`;
  return (
    <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4 shadow-sm">
      <svg viewBox={`0 0 ${w} ${h}`} className="h-32 w-full" preserveAspectRatio="none" role="img">
        <path d={area} className="fill-blue-500/10" />
        <path d={line} className="stroke-blue-500" strokeWidth={2} fill="none" vectorEffect="non-scaling-stroke" />
      </svg>
      <div className="mt-2 flex justify-between text-xs text-gray-500 dark:text-gray-400">
        <span>{`${formatBytes(points[0].totalBytes)} · ${timeAgo(points[0].at)}`}</span>
        <span>{`${formatBytes(points[points.length - 1].totalBytes)} · ${t("databases.table.series.now")}`}</span>
      </div>
    </div>
  );
}

function IndexRow({ idx }: { idx: DatabaseTableIndex }) {
  const { t } = useT();
  return (
    <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b border-gray-100 dark:border-gray-800 px-4 py-3 last:border-0">
      <span className="font-mono text-sm text-gray-900 dark:text-gray-100">{idx.name}</span>
      {idx.isPrimary && (
        <span className="rounded bg-gray-100 dark:bg-gray-800 px-1.5 py-0.5 text-xs text-gray-600 dark:text-gray-300">
          {t("databases.table.indexes.primary")}
        </span>
      )}
      {!idx.isPrimary && idx.isUnique && (
        <span className="rounded bg-gray-100 dark:bg-gray-800 px-1.5 py-0.5 text-xs text-gray-600 dark:text-gray-300">
          {t("databases.table.indexes.unique")}
        </span>
      )}
      {idx.neverScanned && (
        <span className="rounded bg-amber-100 dark:bg-amber-950/50 px-1.5 py-0.5 text-xs text-amber-700 dark:text-amber-400">
          {t("databases.table.indexes.neverScanned")}
        </span>
      )}
      <span className="ml-auto text-sm text-gray-600 dark:text-gray-300">{formatBytes(idx.sizeBytes)}</span>
      <span className="w-full text-xs text-gray-500 dark:text-gray-400 sm:w-auto sm:basis-full">
        {idx.scansInWindow !== null
          ? t("databases.table.indexes.scansInWindow", { scans: count(idx.scansInWindow), hours: String(Math.round(idx.windowHours)) })
          : t("databases.table.indexes.scansTotal", { scans: count(idx.totalScans) })}
      </span>
    </div>
  );
}

function QueryRow({ q }: { q: DatabaseTableQuery }) {
  const { t } = useT();
  return (
    <div className="border-b border-gray-100 dark:border-gray-800 px-4 py-3 last:border-0">
      <pre className="overflow-x-auto whitespace-pre-wrap break-all font-mono text-xs text-gray-800 dark:text-gray-200">{q.query}</pre>
      <div className="mt-2 flex flex-wrap gap-x-4 text-xs text-gray-500 dark:text-gray-400">
        <span>{t("databases.insights.queries.mean", { ms: ms(q.meanMs) })}</span>
        <span>{t("databases.insights.queries.calls", { calls: count(q.calls) })}</span>
        <span>{t("databases.table.queries.total", { ms: ms(q.totalMs) })}</span>
      </div>
    </div>
  );
}

export default function DatabaseTablePage() {
  const params = useParams();
  const search = useSearchParams();
  const { t } = useT();
  const { project, selectedEnv } = useProjectContext();

  const projectId = String(params.projectId);
  const name = String(params.name);
  const table = decodeURIComponent(String(params.table));
  const schema = search.get("schema") || "public";
  const envId = search.get("envId") || selectedEnv?.id || "";

  const [data, setData] = useState<DatabaseTableDetailResponse | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!envId) return;
    let alive = true;
    setLoading(true);
    databasesApi
      .table(projectId, envId, name, table, schema)
      .then((r) => {
        if (alive) setData(r);
      })
      .catch((e: Error) => {
        if (alive) setError(e.message);
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [projectId, envId, name, table, schema]);

  const dbHref = `/projects/${projectId}/databases/${name}${envId ? `?envId=${envId}` : ""}`;

  return (
    <div>
      <Breadcrumb
        items={[
          { label: t("common.crumb.projects"), href: "/projects" },
          { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
          { label: t("nav.databases"), href: `/projects/${projectId}/databases${envId ? `?env=${envId}` : ""}` },
          { label: name, href: dbHref },
          { label: table },
        ]}
      />
      <h1 className="mt-2 font-mono text-2xl font-bold text-gray-900 dark:text-gray-100">
        {schema === "public" ? table : `${schema}.${table}`}
      </h1>
      <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("databases.table.subtitle", { db: name })}</p>

      {loading && (
        <div className="mt-8 flex justify-center">
          <Spinner />
        </div>
      )}

      {!loading && error && (
        <div className="mt-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
          {error}
        </div>
      )}

      {!loading && data && (
        <>
          <div className="mt-6 grid grid-cols-2 gap-4 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm sm:grid-cols-4">
            <Stat
              label={t("databases.table.stat.size")}
              value={formatBytes(data.table.totalBytes)}
              sub={t("databases.table.stat.sizeSub", {
                heap: formatBytes(data.table.heapBytes),
                indexes: formatBytes(data.table.indexBytes),
              })}
            />
            <Stat
              label={t("databases.table.stat.rows")}
              value={count(data.table.liveRows || data.table.rowsEstimate)}
              sub={data.table.deadRows > 0 ? t("databases.table.stat.dead", { rows: count(data.table.deadRows) }) : undefined}
            />
            <Stat
              label={t("databases.insights.stat.growth")}
              value={
                data.table.growthBytes !== undefined && data.table.growthBytes !== null
                  ? `+${formatBytes(data.table.growthBytes)}`
                  : "—"
              }
              sub={
                data.table.windowHours > 0
                  ? t("databases.table.stat.window", { hours: String(Math.round(data.table.windowHours)) })
                  : t("databases.table.stat.noWindow")
              }
            />
            <Stat
              label={t("databases.insights.stat.cacheHit")}
              value={data.table.cacheHitRatio !== undefined && data.table.cacheHitRatio !== null ? pct(data.table.cacheHitRatio) : "—"}
              sub={
                data.table.seqScans !== undefined && data.table.seqScans !== null
                  ? t("databases.table.stat.scans", {
                      seq: count(data.table.seqScans),
                      index: count(data.table.indexScans ?? 0),
                    })
                  : undefined
              }
            />
          </div>

          <p className="mt-2 text-xs text-gray-400 dark:text-gray-500">
            {data.table.sampleStale
              ? t("databases.insights.stale", { ago: timeAgo(data.table.collectedAt) })
              : t("databases.insights.collected", { ago: timeAgo(data.table.collectedAt) })}
            {data.table.lastAutovacuum
              ? ` · ${t("databases.table.vacuumed", { ago: timeAgo(data.table.lastAutovacuum) })}`
              : ` · ${t("databases.table.neverVacuumed")}`}
          </p>

          {data.series.length >= 2 && (
            <Section title={t("databases.table.series.title")}>
              <SizeSparkline points={data.series} />
            </Section>
          )}

          {data.advisories.length > 0 && (
            <Section title={t("databases.insights.advisories.title")}>
              <div className="space-y-3">
                {data.advisories.map((a) => (
                  <AdvisoryCard key={`${a.code}:${a.subject}`} a={a} />
                ))}
              </div>
            </Section>
          )}

          <Section title={t("databases.table.indexes.title")}>
            <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 shadow-sm">
              {data.indexes.length === 0 ? (
                <p className="px-4 py-6 text-sm text-gray-500 dark:text-gray-400">{t("databases.table.indexes.empty")}</p>
              ) : (
                data.indexes.map((idx) => <IndexRow key={idx.name} idx={idx} />)
              )}
            </div>
          </Section>

          <Section title={t("databases.table.queries.title")}>
            <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 shadow-sm">
              {data.queries.length === 0 ? (
                <p className="px-4 py-6 text-sm text-gray-500 dark:text-gray-400">{t("databases.table.queries.empty")}</p>
              ) : (
                data.queries.map((q) => <QueryRow key={q.queryId} q={q} />)
              )}
            </div>
            <p className="mt-2 text-xs text-gray-400 dark:text-gray-500">{t("databases.table.queries.hint")}</p>
          </Section>

          <p className="mt-8 text-xs text-gray-400 dark:text-gray-500">{t("databases.table.noColumns")}</p>
        </>
      )}
    </div>
  );
}

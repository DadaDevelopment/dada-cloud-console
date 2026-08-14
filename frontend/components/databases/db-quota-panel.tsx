"use client";

import Link from "next/link";
import { useCallback, useEffect, useMemo, useState } from "react";
import { databasesApi } from "@/lib/api";
import type {
  DatabaseArchivePlan,
  DatabaseArchiveRun,
  DatabaseInsights,
  DatabaseTableCard,
} from "@/lib/types";
import { formatBytes } from "@/components/charts/format";
import { CopyButton } from "@/components/ui/copy-button";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";

/** Phases the archive worker reports while a run is still moving. */
const openPhases = new Set(["pending", "sink", "export", "verify", "delete", "repack"]);

/** How often an open run is polled, in ms. An archive takes minutes, not seconds. */
const runPollMs = 15_000;

function isoDate(d: Date): string {
  return d.toISOString().slice(0, 10);
}

function humanDate(iso: string, locale: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return new Intl.DateTimeFormat(locale === "ru" ? "ru-RU" : "en-US", {
    day: "numeric",
    month: "long",
    year: "numeric",
  }).format(d);
}

function humanDateTime(iso: string, locale: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return new Intl.DateTimeFormat(locale === "ru" ? "ru-RU" : "en-US", {
    day: "numeric",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  }).format(d);
}

/**
 * hoursLeft is what the banner counts down. Grace is a day, so hours is the
 * unit that stays honest at both ends of it.
 */
function hoursLeft(until: string, now: number): number {
  const ms = new Date(until).getTime() - now;
  return ms <= 0 ? 0 : Math.ceil(ms / 3_600_000);
}

/**
 * archiveCandidate picks the table the Archive button opens on: the largest
 * append-only one, and failing that simply the largest.
 *
 * Append-only is the shape the whole feature is for -- a table that only ever
 * grows is one whose old rows nobody updates, so moving them to Parquet loses
 * nothing. The detector already runs and its verdict is on the table card, so
 * the console does not guess here.
 */
export function archiveCandidate(tables: DatabaseTableCard[]): DatabaseTableCard | undefined {
  const byBytes = [...tables].sort((a, b) => b.totalBytes - a.totalBytes);
  return byBytes.find((t) => t.appendOnly) ?? byBytes[0];
}

/**
 * QuotaBanner is the loud part: the state of the quota said in one sentence,
 * with the deadline when there is one.
 *
 * It renders nothing while the database is comfortably inside its quota. Above
 * the warning ratio it explains what will happen; inside a grace window it
 * counts down; once read-only it says so plainly, since at that point the
 * application's writes are already failing and the owner needs the cause, not
 * a hint.
 */
function QuotaBanner({
  insights,
  now,
  onArchive,
  archivable,
}: {
  insights: DatabaseInsights;
  now: number;
  onArchive?: () => void;
  archivable: boolean;
}) {
  const { t, locale } = useT();
  const limit = insights.sizeLimitBytes ?? 0;
  const size = insights.sizeBytes ?? 0;
  const state = insights.quotaState ?? "none";
  const grace = insights.graceUntil ?? null;
  const warnAt = insights.warnRatio ?? 0.8;
  if (limit <= 0) return null;
  const ratio = size / limit;
  if (state === "none" && !grace && ratio < warnAt) return null;

  const readOnly = state === "read-only" || state === "frozen";
  const tone = readOnly
    ? "border-red-300 dark:border-red-900 bg-red-50 dark:bg-red-950/30"
    : grace
      ? "border-amber-300 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/30"
      : "border-amber-200 dark:border-amber-900/60 bg-amber-50/60 dark:bg-amber-950/20";
  const headline = readOnly
    ? t("databases.quota.banner.readOnly", { db: insights.database ?? "" })
    : grace
      ? t("databases.quota.banner.grace", {
          hours: String(hoursLeft(grace, now)),
          until: humanDateTime(grace, locale),
        })
      : t("databases.quota.banner.warn", { pct: String(Math.round(ratio * 100)) });

  return (
    <div className={`rounded-xl border px-5 py-4 shadow-sm ${tone}`}>
      <p className="text-sm font-semibold text-gray-900 dark:text-gray-100">{headline}</p>
      <p className="mt-1 text-sm text-gray-700 dark:text-gray-300">
        {t("databases.quota.banner.usage", { used: formatBytes(size), limit: formatBytes(limit) })}
        {" "}
        {readOnly
          ? t("databases.quota.banner.readOnlyBody")
          : t("databases.quota.banner.body")}
      </p>
      <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
        {t("databases.quota.banner.backupsSafe")}
      </p>
      <div className="mt-3 flex flex-wrap items-center gap-3">
        <Link
          href="/pricing"
          className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white shadow-sm transition hover:bg-indigo-700"
        >
          {t("databases.quota.banner.upgrade")}
        </Link>
        {archivable && onArchive && (
          <button
            type="button"
            onClick={onArchive}
            className="rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-4 py-2 text-sm font-medium text-gray-800 dark:text-gray-200 transition hover:border-gray-400 dark:hover:border-gray-600"
          >
            {t("databases.quota.banner.archive")}
          </button>
        )}
      </div>
    </div>
  );
}

/**
 * ArchiveDialog is the preview and the confirmation in one: which table, which
 * date, how many rows leave, how much comes back.
 *
 * The cutoff defaults to a month back and every change re-asks the backend for
 * an exact row count, because "about a million rows" is not a number anyone
 * should press a delete button on. Nothing here deletes anything directly --
 * it queues a run whose export is verified against the written Parquet before
 * a single row is removed.
 */
function ArchiveDialog({
  projectId,
  envId,
  name,
  table,
  now,
  onClose,
  onQueued,
}: {
  projectId: string;
  envId: string;
  name: string;
  table: DatabaseTableCard;
  now: number;
  onClose: () => void;
  onQueued: (run: DatabaseArchiveRun) => void;
}) {
  const { t, locale } = useT();
  const [cutoff, setCutoff] = useState(() => {
    const d = new Date();
    d.setUTCMonth(d.getUTCMonth() - 1);
    d.setUTCDate(1);
    return isoDate(d);
  });
  const [planned, setPlanned] = useState<{ cutoff: string; plan: DatabaseArchivePlan | null } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const plan = planned && planned.cutoff === cutoff ? planned.plan : null;
  const loading = !planned || planned.cutoff !== cutoff;

  useEffect(() => {
    let cancelled = false;
    databasesApi
      .archivePlan(projectId, envId, name, table.name, { schema: table.schema, cutoff })
      .then((p) => {
        if (cancelled) return;
        setPlanned({ cutoff, plan: p });
        setError(null);
      })
      .catch((e) => {
        if (cancelled) return;
        setPlanned({ cutoff, plan: null });
        setError(e instanceof Error ? e.message : t("databases.quota.archive.planFailed"));
      });
    return () => {
      cancelled = true;
    };
  }, [projectId, envId, name, table.name, table.schema, cutoff, t]);

  async function submit() {
    if (submitting) return;
    setSubmitting(true);
    try {
      const run = await databasesApi.startArchive(projectId, envId, name, {
        table: table.name,
        schema: table.schema,
        cutoff,
      });
      onQueued(run);
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : t("databases.quota.archive.startFailed"));
      setSubmitting(false);
    }
  }

  const rows = plan?.cutoffRows ?? 0;
  const canSubmit = Boolean(plan?.archivable) && rows > 0 && !submitting;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="dialog" aria-modal="true">
      <div className="w-full max-w-lg rounded-2xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-xl">
        <h3 className="text-base font-semibold text-gray-900 dark:text-gray-100">
          {t("databases.quota.archive.title", { table: `${table.schema}.${table.name}` })}
        </h3>
        <p className="mt-1 text-sm text-gray-600 dark:text-gray-400">{t("databases.quota.archive.body")}</p>

        <label className="mt-4 block text-xs font-medium text-gray-500 dark:text-gray-400" htmlFor="archive-cutoff">
          {t("databases.quota.archive.cutoffLabel")}
        </label>
        <input
          id="archive-cutoff"
          type="date"
          value={cutoff}
          max={now > 0 ? isoDate(new Date(now - 86_400_000)) : undefined}
          onChange={(e) => setCutoff(e.target.value)}
          className="mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-sm text-gray-900 dark:text-gray-100"
        />

        <div className="mt-4 rounded-xl border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-950 px-4 py-3">
          {loading ? (
            <div className="flex h-12 items-center justify-center">
              <Spinner size="sm" />
            </div>
          ) : plan && !plan.archivable ? (
            <p className="text-sm text-amber-700 dark:text-amber-400">{plan.reason}</p>
          ) : plan ? (
            <>
              <p className="text-sm text-gray-900 dark:text-gray-100">
                {t("databases.quota.archive.preview", {
                  rows: rows.toLocaleString(locale === "ru" ? "ru-RU" : "en-US"),
                  date: humanDate(cutoff, locale),
                  size: plan.cutoffBytesEstimateHuman ?? formatBytes(plan.cutoffBytesEstimate ?? 0),
                })}
              </p>
              <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {t("databases.quota.archive.remaining", {
                  rows: (plan.remainingRows ?? 0).toLocaleString(locale === "ru" ? "ru-RU" : "en-US"),
                  column: plan.column?.name ?? "",
                })}
              </p>
            </>
          ) : null}
        </div>

        {error && <p className="mt-3 text-sm text-red-600 dark:text-red-400">{error}</p>}

        <div className="mt-5 flex flex-wrap items-center justify-end gap-3">
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-gray-200"
          >
            {t("databases.quota.archive.cancel")}
          </button>
          <button
            type="button"
            disabled={!canSubmit}
            onClick={submit}
            className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white shadow-sm transition hover:bg-indigo-700 disabled:opacity-50"
          >
            {submitting ? t("databases.quota.archive.starting") : t("databases.quota.archive.confirm")}
          </button>
        </div>
      </div>
    </div>
  );
}

/**
 * RunRow is one archive in the history. An open run shows its phase; a finished
 * one shows what it freed and where the Parquet went, with the two one-liners
 * that read it back -- the promise the feature makes is that the rows are still
 * reachable, and this is where that promise is kept.
 */
function RunRow({ run }: { run: DatabaseArchiveRun }) {
  const { t, locale } = useT();
  const [open, setOpen] = useState(false);
  const running = openPhases.has(run.phase);
  const duck = `SELECT * FROM read_parquet('${run.s3Uri}');`;
  const py = `pandas.read_parquet('${run.s3Uri}')`;
  return (
    <div className="border-t border-gray-100 dark:border-gray-800 py-3 first:border-t-0">
      <div className="flex flex-wrap items-baseline gap-2">
        <span className="font-mono text-sm text-gray-900 dark:text-gray-100">{run.table}</span>
        <span className="text-xs text-gray-500 dark:text-gray-400">
          {t("databases.quota.runs.upTo", { date: humanDate(run.cutoff, locale) })}
        </span>
        {run.auto && (
          <span className="rounded-md bg-gray-100 dark:bg-gray-800 px-2 py-0.5 text-xs text-gray-600 dark:text-gray-400">
            {t("databases.quota.runs.auto")}
          </span>
        )}
        <span className="ml-auto text-xs text-gray-400 dark:text-gray-500">
          {humanDateTime(run.createdAt, locale)}
        </span>
      </div>
      <p className="mt-1 text-sm text-gray-600 dark:text-gray-400">
        {run.phase === "failed" ? (
          <span className="text-red-600 dark:text-red-400">
            {t("databases.quota.runs.failed", { error: run.error ?? "" })}
          </span>
        ) : running ? (
          t(`databases.quota.phase.${run.phase}`)
        ) : (
          t("databases.quota.runs.done", {
            rows: run.deletedRows.toLocaleString(locale === "ru" ? "ru-RU" : "en-US"),
            freed: run.freedHuman || formatBytes(run.bytesFreed),
          })
        )}
      </p>
      {run.phase === "done" && run.s3Uri && (
        <>
          <button
            type="button"
            onClick={() => setOpen((v) => !v)}
            className="mt-1 text-sm font-medium text-indigo-600 hover:text-indigo-800 dark:text-indigo-400 dark:hover:text-indigo-300"
          >
            {open ? t("databases.quota.runs.hideHow") : t("databases.quota.runs.showHow")}
          </button>
          {open && (
            <div className="mt-2 space-y-2">
              <p className="text-xs text-gray-500 dark:text-gray-400">{t("databases.quota.runs.howBody")}</p>
              {[duck, py].map((snippet) => (
                <div key={snippet} className="flex items-center gap-2">
                  <code className="flex-1 break-all rounded-md border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-950 px-3 py-2 font-mono text-xs text-gray-800 dark:text-gray-200">
                    {snippet}
                  </code>
                  <CopyButton value={snippet} />
                </div>
              ))}
            </div>
          )}
        </>
      )}
    </div>
  );
}

/**
 * DbQuotaPanel is the quota half of the database page: the banner, the archive
 * action, and the history of what has already been archived.
 *
 * It loads its own history and swallows the failure, because a database that
 * has never been archived and a control plane that cannot answer look the same
 * from here and neither is worth an error on a page about something else. An
 * open run is polled, since an archive finishes on its own schedule and the
 * owner should see the outcome without reloading.
 */
export function DbQuotaPanel({
  projectId,
  envId,
  name,
  insights,
  tables,
  now,
}: {
  projectId: string;
  envId?: string;
  name: string;
  insights: DatabaseInsights;
  tables: DatabaseTableCard[];
  now: number;
}) {
  const { t } = useT();
  const [runs, setRuns] = useState<DatabaseArchiveRun[]>([]);
  const [dialog, setDialog] = useState(false);
  const candidate = useMemo(() => archiveCandidate(tables), [tables]);

  const load = useCallback(() => {
    if (!envId) return;
    databasesApi
      .archiveRuns(projectId, envId, name)
      .then((r) => setRuns(r.runs ?? []))
      .catch(() => undefined);
  }, [projectId, envId, name]);

  useEffect(() => {
    load();
  }, [load]);

  const hasOpen = runs.some((r) => openPhases.has(r.phase));
  useEffect(() => {
    if (!hasOpen) return;
    const id = setInterval(load, runPollMs);
    return () => clearInterval(id);
  }, [hasOpen, load]);

  const banner = (
    <QuotaBanner
      insights={insights}
      now={now}
      archivable={Boolean(candidate && envId && !hasOpen)}
      onArchive={() => setDialog(true)}
    />
  );

  return (
    <div className="space-y-4">
      {banner}
      {runs.length > 0 && (
      <div>
        <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
          {t("databases.quota.runs.title")}
        </h3>
        <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-4 py-2 shadow-sm">
          {runs.slice(0, 5).map((r) => (
            <RunRow key={r.id} run={r} />
          ))}
        </div>
      </div>
      )}
      {dialog && candidate && envId && (
        <ArchiveDialog
          projectId={projectId}
          envId={envId}
          name={name}
          table={candidate}
          now={now}
          onClose={() => setDialog(false)}
          onQueued={(run) => setRuns((prev) => [run, ...prev])}
        />
      )}
    </div>
  );
}

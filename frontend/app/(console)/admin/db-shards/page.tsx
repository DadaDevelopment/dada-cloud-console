"use client";
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { adminApi } from "@/lib/api";
import type { AdminDBShard, AdminDBShardDatabase, AdminDBShardsResponse } from "@/lib/types";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { AdminTabs } from "@/components/console/admin-tabs";
import { Card, CardContent } from "@/components/ui/card";
import { formatBytes } from "@/components/charts/format";
import { timeAgo } from "@/lib/format";
import { useT } from "@/lib/i18n/console/context";

const REFRESH_MS = 60_000;

function uptime(from: string | undefined): string {
  if (!from) return "—";
  const days = (Date.now() - new Date(from).getTime()) / 86_400_000;
  if (days >= 1) return `${Math.floor(days)}d`;
  return `${Math.max(1, Math.round(days * 24))}h`;
}

function ShardStat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{label}</p>
      <p className="mt-0.5 text-sm font-semibold text-gray-900 dark:text-gray-100">{value}</p>
    </div>
  );
}

function DatabaseRow({ db, t }: { db: AdminDBShardDatabase; t: (k: string) => string }) {
  const owner = db.orphan ? null : (
    <Link
      href={`/projects/${db.project_id}/databases/${db.resource}`}
      className="text-blue-600 hover:underline dark:text-blue-400"
    >
      {db.project_name || db.project_id}
      {db.resource ? ` / ${db.resource}` : ""}
    </Link>
  );
  return (
    <tr className="border-t border-gray-100 dark:border-gray-800">
      <td className="py-2 pr-3 font-mono text-xs text-gray-900 dark:text-gray-100">
        {db.datname}
        {db.tier && <span className="ml-2 text-[10px] uppercase text-gray-400">{db.tier}</span>}
      </td>
      <td className="py-2 pr-3 text-xs">
        {owner ?? (
          <span
            className="rounded bg-red-50 px-1.5 py-0.5 font-medium text-red-700 dark:bg-red-950/40 dark:text-red-400"
            title={t("adminDbShards.orphan.hint")}
          >
            {t("adminDbShards.orphan")}
          </span>
        )}
      </td>
      <td className="py-2 pr-3 text-right text-xs tabular-nums text-gray-900 dark:text-gray-100">
        {formatBytes(db.size_bytes)}
      </td>
      <td className="py-2 pr-3 text-right text-xs tabular-nums text-gray-500 dark:text-gray-400">
        {`${Math.round(db.share * 100)}%`}
      </td>
      <td className="py-2 pr-3 text-right text-xs tabular-nums text-gray-500 dark:text-gray-400">
        {db.growth_bytes_7d > 0 ? `+${formatBytes(db.growth_bytes_7d)}` : "—"}
      </td>
      <td className="py-2 pr-3 text-right text-xs tabular-nums text-gray-500 dark:text-gray-400">{db.connections}</td>
      <td className="py-2 text-right text-xs tabular-nums">
        {db.critical_advisories > 0 && (
          <span className="mr-1 rounded bg-red-100 px-1.5 py-0.5 font-medium text-red-700 dark:bg-red-950/50 dark:text-red-400">
            {db.critical_advisories}
          </span>
        )}
        {db.warning_advisories > 0 && (
          <span className="rounded bg-amber-100 px-1.5 py-0.5 font-medium text-amber-700 dark:bg-amber-950/50 dark:text-amber-400">
            {db.warning_advisories}
          </span>
        )}
        {db.critical_advisories === 0 && db.warning_advisories === 0 && (
          <span className="text-gray-400">—</span>
        )}
      </td>
    </tr>
  );
}

function ShardCard({ shard, windowDays }: { shard: AdminDBShard; windowDays: number }) {
  const { t } = useT();
  return (
    <Card>
      <CardContent className="p-4">
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <h2 className="font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{shard.name}</h2>
          <span className="rounded bg-gray-100 px-1.5 py-0.5 text-[11px] text-gray-600 dark:bg-gray-800 dark:text-gray-300">
            {t(`adminDbShards.state.${shard.state}`)}
          </span>
          {shard.is_platform && (
            <span className="rounded bg-purple-100 px-1.5 py-0.5 text-[11px] text-purple-700 dark:bg-purple-950/50 dark:text-purple-300">
              {t("adminDbShards.platform")}
            </span>
          )}
        </div>

        <div className="mb-4 grid grid-cols-2 gap-4 sm:grid-cols-5">
          <ShardStat label={t("adminDbShards.stat.sampled")} value={formatBytes(shard.sampled_bytes)} />
          <ShardStat
            label={t("adminDbShards.stat.capacity")}
            value={shard.capacity_bytes > 0 ? formatBytes(shard.capacity_bytes) : t("adminDbShards.capacity.unbounded")}
          />
          <ShardStat label={t("adminDbShards.stat.databases")} value={String(shard.databases)} />
          <ShardStat label={t("adminDbShards.stat.uptime")} value={uptime(shard.instance_start_at)} />
          <ShardStat
            label={t("adminDbShards.stat.collected")}
            value={shard.collected_at ? timeAgo(shard.collected_at) : "—"}
          />
        </div>

        {shard.top.length === 0 ? (
          <p className="text-xs text-gray-500 dark:text-gray-400">{t("adminDbShards.noSamples")}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[640px] text-left">
              <thead>
                <tr className="text-[11px] uppercase tracking-wide text-gray-400 dark:text-gray-500">
                  <th className="pb-1 pr-3 font-medium">{t("adminDbShards.col.database")}</th>
                  <th className="pb-1 pr-3 font-medium">{t("adminDbShards.col.owner")}</th>
                  <th className="pb-1 pr-3 text-right font-medium">{t("adminDbShards.col.size")}</th>
                  <th className="pb-1 pr-3 text-right font-medium">{t("adminDbShards.col.share")}</th>
                  <th className="pb-1 pr-3 text-right font-medium">
                    {t("adminDbShards.window").replace("{days}", String(windowDays))}
                  </th>
                  <th className="pb-1 pr-3 text-right font-medium">{t("adminDbShards.col.conns")}</th>
                  <th className="pb-1 text-right font-medium">{t("adminDbShards.col.advisories")}</th>
                </tr>
              </thead>
              <tbody>
                {shard.top.map((db) => (
                  <DatabaseRow key={db.datname} db={db} t={t} />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

export default function AdminDBShardsPage() {
  const { t } = useT();
  const [data, setData] = useState<AdminDBShardsResponse | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [forbidden, setForbidden] = useState(false);

  const load = useCallback(async (opts: { silent?: boolean } = {}) => {
    if (!opts.silent) setIsLoading(true);
    try {
      setData(await adminApi.getDBShards());
      setForbidden(false);
      setError(null);
    } catch (err) {
      const status = (err as { status?: number } | undefined)?.status;
      if (status === 403) {
        setForbidden(true);
      } else {
        setError(err instanceof Error ? err.message : t("adminDbShards.error.load"));
      }
    } finally {
      setIsLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (forbidden) return;
    const interval = setInterval(() => { void load(); }, REFRESH_MS);
    return () => clearInterval(interval);
  }, [forbidden, load]);

  const crumb = (
    <Breadcrumb
      items={[
        { label: t("common.crumb.console"), href: "/projects" },
        { label: t("approvals.crumb.admin") },
        { label: t("adminDbShards.crumb.dbShards") },
      ]}
    />
  );

  if (forbidden) {
    return (
      <div>
        {crumb}
        <div className="mt-4 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300">
          {t("adminDbShards.accessDenied")}
        </div>
      </div>
    );
  }

  const shards = data?.shards ?? [];

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div>
          {crumb}
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("adminDbShards.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("adminDbShards.subtitle")}</p>
        </div>
        <button
          onClick={() => load()}
          className="inline-flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 shadow-sm transition-colors hover:border-blue-300 hover:text-blue-600 dark:border-gray-800 dark:bg-gray-900 dark:text-gray-200"
        >
          {t("common.refresh")}
        </button>
      </div>

      <AdminTabs active="db-shards" />

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-400">
          {error}
        </div>
      )}

      {!isLoading && shards.every((s) => s.databases === 0) && (
        <div className="mb-6 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300">
          {t("adminDbShards.empty")}
        </div>
      )}

      <div className="space-y-4">
        {shards.map((shard) => (
          <ShardCard key={shard.name} shard={shard} windowDays={data?.window_days ?? 7} />
        ))}
      </div>
    </div>
  );
}

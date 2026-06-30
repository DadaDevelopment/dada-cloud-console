"use client";
import { useMemo, useState } from "react";
import { Search, Plus } from "lucide-react";
import { EChart } from "@/components/charts/echart";
import { buildSparkline } from "@/components/charts/builders";
import { formatValue, inferUnit } from "@/components/charts/format";
import type { MonitoringMetricSpec } from "@/lib/types";

/**
 * MetricsExplorer lists every discovered metric with a live value + sparkline and
 * a one-click add to the dashboard. Search filters by metric name. This is the
 * "what can I chart?" surface that complements the curated dashboard grid.
 */
export function MetricsExplorer({
  metrics,
  onAdd,
}: {
  metrics: Record<string, MonitoringMetricSpec>;
  onAdd: (metric: string) => void;
}) {
  const [q, setQ] = useState("");
  const names = useMemo(() => {
    const all = Object.keys(metrics).sort();
    if (!q.trim()) return all;
    const needle = q.toLowerCase();
    return all.filter((n) => n.toLowerCase().includes(needle));
  }, [metrics, q]);

  return (
    <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 shadow-sm">
      <div className="flex items-center gap-2 border-b border-gray-100 dark:border-gray-800 px-4 py-3">
        <Search className="h-4 w-4 text-gray-400 dark:text-gray-500" />
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search metrics…"
          className="w-full bg-transparent text-sm text-gray-900 dark:text-gray-100 outline-none placeholder:text-gray-400"
        />
        <span className="shrink-0 text-xs text-gray-400 dark:text-gray-500">{names.length} metrics</span>
      </div>
      <div className="max-h-96 divide-y divide-gray-50 overflow-y-auto">
        {names.length === 0 ? (
          <p className="px-4 py-8 text-center text-sm text-gray-400 dark:text-gray-500">No metrics match.</p>
        ) : (
          names.map((name, i) => {
            const spec = metrics[name];
            const pts = spec.series[0]?.points ?? [];
            const current = pts.length ? pts[pts.length - 1].v : NaN;
            const unit = spec.unit || inferUnit(name);
            return (
              <div key={name} className="flex items-center gap-3 px-4 py-2.5 hover:bg-gray-50">
                <div className="min-w-0 flex-1">
                  <p className="truncate font-mono text-xs text-gray-800 dark:text-gray-200">{name}</p>
                  <p className="text-[10px] uppercase tracking-wide text-gray-400 dark:text-gray-500">
                    {spec.kind ?? "gauge"} · {spec.series.length} series
                  </p>
                </div>
                <div className="hidden h-7 w-24 shrink-0 sm:block">
                  <EChart option={buildSparkline(spec.series, i)} height={28} bare aria-label={`${name} trend`} />
                </div>
                <span className="w-20 shrink-0 text-right text-sm font-semibold text-gray-900 dark:text-gray-100">
                  {formatValue(current, unit)}
                </span>
                <button
                  onClick={() => onAdd(name)}
                  title="Add to dashboard"
                  className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg border border-gray-200 dark:border-gray-800 text-gray-500 dark:text-gray-400 hover:border-blue-300 hover:text-blue-600"
                >
                  <Plus className="h-3.5 w-3.5" />
                </button>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}

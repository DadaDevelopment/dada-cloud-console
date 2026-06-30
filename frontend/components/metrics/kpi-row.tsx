"use client";
import { useMemo } from "react";
import { ArrowUpRight, ArrowDownRight, Minus } from "lucide-react";
import { EChart } from "@/components/charts/echart";
import { buildSparkline } from "@/components/charts/builders";
import { formatValue, inferUnit } from "@/components/charts/format";
import type { MonitoringMetricSpec } from "@/lib/types";
import { cn } from "@/lib/cn";

interface Kpi {
  metric: string;
  current: number;
  deltaPct: number | null;
  unit: string;
  spec: MonitoringMetricSpec;
}

function computeKpi(metric: string, spec: MonitoringMetricSpec): Kpi | null {
  const pts = spec.series[0]?.points ?? [];
  if (pts.length === 0) return null;
  const current = pts[pts.length - 1].v;
  let deltaPct: number | null = null;
  if (pts.length >= 4) {
    const mid = Math.floor(pts.length / 2);
    const avg = (arr: typeof pts) => arr.reduce((a, p) => a + p.v, 0) / (arr.length || 1);
    const prev = avg(pts.slice(0, mid));
    const cur = avg(pts.slice(mid));
    if (prev !== 0) deltaPct = ((cur - prev) / Math.abs(prev)) * 100;
  }
  return { metric, current, deltaPct, unit: spec.unit || inferUnit(metric), spec };
}

export function KpiRow({ metrics }: { metrics: Record<string, MonitoringMetricSpec> }) {
  const kpis = useMemo(() => {
    return Object.keys(metrics)
      .sort()
      .slice(0, 6)
      .map((m) => computeKpi(m, metrics[m]))
      .filter((k): k is Kpi => k !== null);
  }, [metrics]);

  if (kpis.length === 0) return null;

  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
      {kpis.map((k, i) => (
        <KpiCard key={k.metric} kpi={k} index={i} />
      ))}
    </div>
  );
}

function KpiCard({ kpi, index }: { kpi: Kpi; index: number }) {
  const { deltaPct } = kpi;
  const up = deltaPct !== null && deltaPct > 0.5;
  const down = deltaPct !== null && deltaPct < -0.5;
  const sparkOption = useMemo(() => buildSparkline(kpi.spec.series, index), [kpi.spec.series, index]);

  return (
    <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-3 shadow-sm transition-shadow hover:shadow-md">
      <p className="truncate font-mono text-[11px] uppercase tracking-wide text-gray-400 dark:text-gray-500" title={kpi.metric}>
        {kpi.metric}
      </p>
      <div className="mt-1 flex items-end justify-between gap-2">
        <span className="text-xl font-semibold text-gray-900 dark:text-gray-100">{formatValue(kpi.current, kpi.unit)}</span>
        {deltaPct !== null && (
          <span
            className={cn(
              "inline-flex items-center gap-0.5 rounded-md px-1 py-0.5 text-[10px] font-semibold",
              up && "bg-amber-50 dark:bg-amber-950/40 text-amber-700 dark:text-amber-300",
              down && "bg-emerald-50 dark:bg-emerald-950/40 text-emerald-700 dark:text-emerald-300",
              !up && !down && "bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400",
            )}
          >
            {up ? <ArrowUpRight className="h-3 w-3" /> : down ? <ArrowDownRight className="h-3 w-3" /> : <Minus className="h-3 w-3" />}
            {Math.abs(deltaPct).toFixed(1)}%
          </span>
        )}
      </div>
      <div className="mt-2 h-8">
        <EChart option={sparkOption} height={32} bare aria-label={`${kpi.metric} trend`} />
      </div>
    </div>
  );
}

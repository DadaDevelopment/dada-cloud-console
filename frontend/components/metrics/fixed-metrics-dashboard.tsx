"use client";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { appServersApi, appsApi } from "@/lib/api";
import type { MetricsResponse, MonitoringMetricSpec } from "@/lib/types";
import { EChart } from "@/components/charts/echart";
import { dispatchBuild } from "@/components/charts/builders";
import { inferUnit, formatValue } from "@/components/charts/format";
import { Spinner } from "@/components/ui/spinner";

const RANGES = ["15m", "1h", "6h", "24h"] as const;
type Range = (typeof RANGES)[number];

interface FixedSpec {
  key: string;
  title: string;
}

/**
 * VM and app metrics come from a fixed collector schema (node_exporter / cAdvisor),
 * so their keys + titles are known ahead of time — unlike native monitoring
 * resources, which discover panels from the response. These two surfaces have no
 * group-by/aggregation/filter, so this dashboard renders a fixed ECharts grid
 * with only a range selector.
 */
const VM_KEYS: FixedSpec[] = [
  { key: "cpu_pct", title: "CPU" },
  { key: "mem_pct", title: "Memory" },
  { key: "disk_pct", title: "Disk" },
  { key: "net_rx", title: "Net in" },
  { key: "net_tx", title: "Net out" },
];
const APP_KEYS: FixedSpec[] = [
  { key: "cpu_cores", title: "CPU (cores)" },
  { key: "mem_bytes", title: "Memory" },
];

/** Adapts the fixed single-series response into the multi-series chart-kit shape. */
function wrapSingle(spec: { unit: string; series: { t: number; v: number }[] } | undefined): MonitoringMetricSpec {
  return { unit: spec?.unit ?? "", series: [{ label: "", points: spec?.series ?? [] }] };
}

type Props =
  | { kind: "vm"; projectId: string; serverName: string }
  | { kind: "app"; projectId: string; envId: string; appName: string };

/**
 * FixedMetricsDashboard is the ECharts surface for VM + app metrics: a fixed panel
 * grid driven by the known collector keys, with a range selector and 30s polling.
 * Replaces the old inline-SVG MetricsPanel/MetricChart for these two surfaces.
 */
export function FixedMetricsDashboard(props: Props) {
  const [range, setRange] = useState<Range>("1h");
  const [data, setData] = useState<MetricsResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const targetKey =
    props.kind === "vm"
      ? `vm:${props.projectId}:${props.serverName}`
      : `app:${props.projectId}:${props.envId}:${props.appName}`;

  const load = useCallback(async () => {
    try {
      const d =
        props.kind === "vm"
          ? await appServersApi.getMetrics(props.projectId, props.serverName, range)
          : await appsApi.getMetrics(props.projectId, props.envId, props.appName, range);
      setData(d);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load metrics");
    } finally {
      setLoading(false);
    }
    // targetKey captures the identity fields of this union.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [range, targetKey]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true);
    void load();
    const id = setInterval(() => void load(), 30000);
    return () => clearInterval(id);
  }, [load]);

  const specs = props.kind === "vm" ? VM_KEYS : APP_KEYS;
  const wideRange = range === "24h";

  return (
    <div className="rounded-xl border border-gray-200 bg-white">
      <div className="flex items-center justify-between border-b border-gray-100 px-5 py-3">
        <h2 className="text-sm font-semibold text-gray-800">Metrics</h2>
        <div className="flex items-center gap-2">
          {data?.live_error && (
            <span title={data.live_error} className="text-xs text-amber-600">
              partial
            </span>
          )}
          <div className="inline-flex rounded-lg border border-gray-200 p-0.5">
            {RANGES.map((r) => (
              <button
                key={r}
                onClick={() => setRange(r)}
                className={`rounded-md px-2 py-1 text-xs font-medium transition-colors ${
                  range === r ? "bg-blue-600 text-white" : "text-gray-500 hover:bg-gray-100"
                }`}
              >
                {r}
              </button>
            ))}
          </div>
        </div>
      </div>

      {loading && !data ? (
        <div className="flex h-32 items-center justify-center">
          <Spinner size="md" />
        </div>
      ) : error ? (
        <div className="px-5 py-4 text-sm text-red-600">{error}</div>
      ) : (
        <div className="grid gap-4 p-5 sm:grid-cols-2 lg:grid-cols-3">
          {specs.map(({ key, title }) => (
            <FixedPanel key={key} title={title} spec={wrapSingle(data?.metrics?.[key])} metric={key} wideRange={wideRange} />
          ))}
        </div>
      )}
    </div>
  );
}

function FixedPanel({
  title,
  spec,
  metric,
  wideRange,
}: {
  title: string;
  spec: MonitoringMetricSpec;
  metric: string;
  wideRange: boolean;
}) {
  const unit = spec.unit || inferUnit(metric);
  const points = spec.series[0]?.points ?? [];
  const hasData = points.length > 0;
  const current = hasData ? points[points.length - 1].v : NaN;

  const option = useMemo(
    () => (hasData ? dispatchBuild({ series: spec.series, viz: "line", unit, wideRange, zoom: false }) : null),
    [spec.series, unit, wideRange, hasData],
  );

  return (
    <div className="rounded-lg border border-gray-200 bg-white p-4">
      <div className="flex items-baseline justify-between">
        <span className="text-xs font-semibold uppercase tracking-wide text-gray-400">{title}</span>
        <span className="text-sm font-semibold text-gray-900">{hasData ? formatValue(current, unit) : "—"}</span>
      </div>
      <div className="mt-2 h-24">
        {option ? (
          <EChart option={option} height="100%" aria-label={title} />
        ) : (
          <div className="flex h-full items-center justify-center text-xs text-gray-300">no data</div>
        )}
      </div>
    </div>
  );
}

const SPARK_W = 64;
const SPARK_H = 40;

function fmtCores(v: number): string {
  if (v >= 1) return `${v.toFixed(2)} cores`;
  return `${(v * 1000).toFixed(0)}m`;
}

/**
 * MetricSparkline is the compact CPU+RAM trend for an app card: two normalized
 * lines in one tiny ECharts canvas with the current absolute values printed
 * alongside. Renders nothing when the app has no metrics. Replaces AppSparkline.
 */
export function MetricSparkline({
  projectId,
  envId,
  appName,
}: {
  projectId: string;
  envId: string;
  appName: string;
}) {
  const [data, setData] = useState<MetricsResponse | null>(null);
  const [failed, setFailed] = useState(false);
  const alive = useRef(true);

  useEffect(() => {
    alive.current = true;
    appsApi
      .getMetrics(projectId, envId, appName, "1h")
      .then((d) => alive.current && setData(d))
      .catch(() => alive.current && setFailed(true));
    return () => {
      alive.current = false;
    };
  }, [projectId, envId, appName]);

  const cpu = data?.metrics?.cpu_cores?.series;
  const mem = data?.metrics?.mem_bytes?.series;
  const hasData = (cpu?.length ?? 0) > 0 || (mem?.length ?? 0) > 0;

  const option = useMemo(() => {
    const norm = (pts: { t: number; v: number }[], color: string) => {
      const max = Math.max(...pts.map((p) => p.v), 0) || 1;
      return {
        type: "line" as const,
        showSymbol: false,
        smooth: 0.3,
        lineStyle: { width: 1.25, color },
        data: pts.map((p) => [p.t * 1000, p.v / max] as [number, number]),
        sampling: "lttb" as const,
      };
    };
    return {
      backgroundColor: "transparent",
      animation: false,
      grid: { left: 1, right: 1, top: 2, bottom: 1 },
      xAxis: { type: "time" as const, show: false },
      yAxis: { type: "value" as const, show: false, min: 0, max: 1 },
      tooltip: { show: false },
      legend: { show: false },
      series: [norm(mem ?? [], "#7c3aed"), norm(cpu ?? [], "#2563eb")],
    };
  }, [cpu, mem]);

  const lastCpu = cpu?.length ? cpu[cpu.length - 1].v : 0;
  const lastMem = mem?.length ? mem[mem.length - 1].v : 0;

  if (failed) return null;
  if (data && !hasData) return null;

  return (
    <div className="mt-2 flex items-center gap-2 border-t border-gray-100 pt-1.5">
      {!data ? (
        <div className="h-10 w-16 animate-pulse rounded bg-gray-50" />
      ) : (
        <>
          <div className="h-10 w-16 shrink-0 rounded bg-gray-50/60" style={{ width: SPARK_W, height: SPARK_H }}>
            <EChart option={option} bare height={SPARK_H} aria-label="CPU and memory, last hour (normalized)" />
          </div>
          <div className="flex shrink-0 flex-col text-[10px] leading-tight text-gray-400">
            <span style={{ color: "#2563eb" }}>{fmtCores(lastCpu)}</span>
            <span style={{ color: "#7c3aed" }}>{formatValue(lastMem, "B")}</span>
          </div>
        </>
      )}
    </div>
  );
}

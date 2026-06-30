"use client";
import { useCallback, useEffect, useState } from "react";
import { appServersApi, appsApi, monitoringApi } from "@/lib/api";
import type {
  MetricsResponse,
  MetricSeries,
  MonitoringLabelsResponse,
  MonitoringMetricsResponse,
} from "@/lib/types";
import { MetricChart } from "@/components/metric-chart";
import { Spinner } from "@/components/ui/spinner";

const RANGES = ["15m", "1h", "6h", "24h"] as const;
type Range = (typeof RANGES)[number];

// VM and app metrics come from a fixed collector schema (node_exporter / cAdvisor),
// so their keys + titles are known ahead of time. Monitoring resources accept
// ARBITRARY metric names from the resource, so their panels are discovered from
// the response at render time — see the monitoring branch below. Never assume
// cpu/mem/temp for monitoring: render exactly what was sent, nothing more.
const VM_KEYS: { key: string; title: string; color?: string }[] = [
  { key: "cpu_pct", title: "CPU" },
  { key: "mem_pct", title: "Memory", color: "#7c3aed" },
  { key: "disk_pct", title: "Disk", color: "#ea580c" },
  { key: "net_rx", title: "Net in", color: "#059669" },
  { key: "net_tx", title: "Net out", color: "#0891b2" },
];
const APP_KEYS: { key: string; title: string; color?: string }[] = [
  { key: "cpu_cores", title: "CPU (cores)" },
  { key: "mem_bytes", title: "Memory", color: "#7c3aed" },
];

// Cosmetic-only: a friendlier title for a few common metric names. NEVER used to
// add, drop, or order panels — only to prettify a discovered key's label.
const MONITORING_TITLES: Record<string, string> = {
  cpu: "CPU",
  memory: "Memory",
  mem: "Memory",
  temperature: "Temperature",
  temp: "Temperature",
};

// Stable color per discovered metric so a panel keeps its color across renders
// regardless of discovery order.
const PALETTE = ["#2563eb", "#7c3aed", "#ea580c", "#059669", "#0891b2", "#db2777", "#ca8a04", "#dc2626"];
function colorForKey(key: string): string {
  let h = 0;
  for (let i = 0; i < key.length; i++) h = (h * 31 + key.charCodeAt(i)) >>> 0;
  return PALETTE[h % PALETTE.length];
}

// wrapSingle adapts the fixed vm/app single-series response into the multi-series
// shape MetricChart now expects, so both feed the same chart component.
function wrapSingle(spec: { unit: string; series: { t: number; v: number }[] } | undefined): {
  unit: string;
  kind?: "counter" | "gauge";
  series: MetricSeries[];
} {
  return { unit: spec?.unit ?? "", series: [{ label: "", points: spec?.series ?? [] }] };
}

export function MetricsPanel(
  props:
    | { kind: "vm"; projectId: string; serverName: string }
    | { kind: "app"; projectId: string; envId: string; appName: string }
    | { kind: "monitoring"; projectId: string; envId: string; appId: string }
) {
  const [range, setRange] = useState<Range>("1h");
  const [fixedData, setFixedData] = useState<MetricsResponse | null>(null);
  const [monData, setMonData] = useState<MonitoringMetricsResponse | null>(null);
  const [labels, setLabels] = useState<MonitoringLabelsResponse>({ labels: {}, names: [] });
  const [groupBy, setGroupBy] = useState("");
  const [filters, setFilters] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const liveError = props.kind === "monitoring" ? monData?.live_error : fixedData?.live_error;

  // Stable identity string for the current target; changes retrigger the fetch.
  const targetKey =
    props.kind === "vm"
      ? `vm:${props.projectId}:${props.serverName}`
      : props.kind === "monitoring"
      ? `monitoring:${props.projectId}:${props.envId}:${props.appId}`
      : `app:${props.projectId}:${props.envId}:${props.appName}`;

  const filterList = Object.entries(filters).map(([k, v]) => `${k}=${v}`);
  const filterKey = filterList.slice().sort().join(",");

  const load = useCallback(async () => {
    try {
      if (props.kind === "monitoring") {
        const d = await monitoringApi.getMetrics(props.projectId, props.envId, props.appId, {
          range,
          groupBy: groupBy || undefined,
          filters: filterList,
        });
        setMonData(d);
      } else if (props.kind === "vm") {
        setFixedData(await appServersApi.getMetrics(props.projectId, props.serverName, range));
      } else {
        setFixedData(await appsApi.getMetrics(props.projectId, props.envId, props.appName, range));
      }
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load metrics");
    } finally {
      setLoading(false);
    }
    // targetKey captures projectId/serverName/envId/appName/appId for this union.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [range, targetKey, groupBy, filterKey]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load();
    const id = setInterval(() => void load(), 30000);
    return () => clearInterval(id);
  }, [load]);

  // Monitoring panels are fully data-driven: discover the selectable label
  // dimensions so the group-by and filter controls reflect exactly what the
  // resource emits, with no fixed dimension set.
  useEffect(() => {
    if (props.kind !== "monitoring") return;
    monitoringApi
      .getLabels(props.projectId, props.envId, props.appId, "24h")
      .then(setLabels)
      .catch(() => setLabels({ labels: {}, names: [] }));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [targetKey, range]);

  // Monitoring panels are fully data-driven: one panel per metric the resource
  // actually sent, discovered from the response. No fixed cpu/mem/temp set.
  let specs: { key: string; title: string; color?: string }[];
  if (props.kind === "monitoring") {
    specs = monData
      ? Object.keys(monData.metrics)
          .sort()
          .map((k) => ({ key: k, title: MONITORING_TITLES[k] ?? k, color: colorForKey(k) }))
      : [];
  } else {
    specs = props.kind === "vm" ? VM_KEYS : APP_KEYS;
  }

  function setFilter(key: string, value: string) {
    setFilters((prev) => {
      const next = { ...prev };
      if (value === "") delete next[key];
      else next[key] = value;
      return next;
    });
  }

  const labelKeys = Object.keys(labels.labels).sort();

  return (
    <div className="rounded-xl border border-gray-200 bg-white">
      <div className="flex items-center justify-between border-b border-gray-100 px-5 py-3">
        <h2 className="text-sm font-semibold text-gray-800">Metrics</h2>
        <div className="flex items-center gap-2">
          {liveError && (
            <span title={liveError} className="text-xs text-amber-600">
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

      {props.kind === "monitoring" && labelKeys.length > 0 && (
        <div className="flex flex-wrap items-center gap-3 border-b border-gray-100 px-5 py-3">
          <label className="flex items-center gap-1.5 text-xs text-gray-500">
            Group by
            <select
              value={groupBy}
              onChange={(e) => setGroupBy(e.target.value)}
              className="rounded-md border border-gray-300 bg-white px-2 py-1 text-xs text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="">None</option>
              {labelKeys.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </select>
          </label>
          {labelKeys.map((k) => (
            <label key={k} className="flex items-center gap-1.5 text-xs text-gray-500">
              Filter {k}
              <select
                value={filters[k] ?? ""}
                onChange={(e) => setFilter(k, e.target.value)}
                className="rounded-md border border-gray-300 bg-white px-2 py-1 text-xs text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              >
                <option value="">All</option>
                {labels.labels[k].map((v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ))}
              </select>
            </label>
          ))}
        </div>
      )}

      {loading && !monData && !fixedData ? (
        <div className="flex h-32 items-center justify-center">
          <Spinner size="md" />
        </div>
      ) : error ? (
        <div className="px-5 py-4 text-sm text-red-600">{error}</div>
      ) : specs.length === 0 ? (
        <div className="flex h-32 flex-col items-center justify-center gap-1 px-5 text-center">
          <p className="text-sm font-medium text-gray-500">No metrics in this range</p>
          <p className="text-xs text-gray-400">
            Charts appear automatically once this resource reports metrics.
          </p>
        </div>
      ) : (
        <div className="grid gap-4 p-5 sm:grid-cols-2 lg:grid-cols-3">
          {specs.map(({ key, title, color }) => {
            const m =
              props.kind === "monitoring"
                ? monData?.metrics?.[key]
                : wrapSingle(fixedData?.metrics?.[key]);
            return (
              <MetricChart
                key={key}
                title={title}
                unit={m?.unit ?? ""}
                kind={m?.kind}
                series={m?.series ?? []}
                color={color}
              />
            );
          })}
        </div>
      )}
    </div>
  );
}

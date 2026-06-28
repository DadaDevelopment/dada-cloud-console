"use client";
import { useCallback, useEffect, useState } from "react";
import { appServersApi, appsApi, monitoringApi } from "@/lib/api";
import type { MetricsResponse } from "@/lib/types";
import { MetricChart } from "@/components/metric-chart";
import { Spinner } from "@/components/ui/spinner";

const RANGES = ["15m", "1h", "6h", "24h"] as const;
type Range = (typeof RANGES)[number];

// VM and app metrics come from a fixed collector schema (node_exporter / cAdvisor),
// so their keys + titles are known ahead of time. Monitoring resources accept
// ARBITRARY metric names from the device, so their panels are discovered from the
// response at render time — see the monitoring branch below. Never assume cpu/mem/
// temp for monitoring: render exactly what the device sent, nothing more.
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

export function MetricsPanel(
  props:
    | { kind: "vm"; projectId: string; serverName: string }
    | { kind: "app"; projectId: string; envId: string; appName: string }
    | { kind: "monitoring"; projectId: string; envId: string; appId: string; source?: string }
) {
  const [range, setRange] = useState<Range>("1h");
  const [data, setData] = useState<MetricsResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  // Stable identity string for the current target; changes retrigger the fetch.
  const targetKey =
    props.kind === "vm"
      ? `vm:${props.projectId}:${props.serverName}`
      : props.kind === "monitoring"
      ? `monitoring:${props.projectId}:${props.envId}:${props.appId}:${props.source ?? ""}`
      : `app:${props.projectId}:${props.envId}:${props.appName}`;

  const load = useCallback(async () => {
    try {
      let d: MetricsResponse;
      if (props.kind === "vm") {
        d = await appServersApi.getMetrics(props.projectId, props.serverName, range);
      } else if (props.kind === "monitoring") {
        d = await monitoringApi.getMetrics(props.projectId, props.envId, props.appId, range, props.source);
      } else {
        d = await appsApi.getMetrics(props.projectId, props.envId, props.appName, range);
      }
      setData(d);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load metrics");
    } finally {
      setLoading(false);
    }
    // targetKey captures projectId/serverName/envId/appName/appId for this union.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [range, targetKey]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load();
    const id = setInterval(() => void load(), 30000);
    return () => clearInterval(id);
  }, [load]);

  // Monitoring panels are fully data-driven: one panel per metric the device
  // actually sent, discovered from the response. No fixed cpu/mem/temp set, so a
  // device emitting arbitrary metrics renders exactly its own keys and an idle
  // device renders an explicit empty state (handled in the render below).
  let specs: { key: string; title: string; color?: string }[];
  if (props.kind === "monitoring") {
    specs = data
      ? Object.keys(data.metrics)
          .sort()
          .map((k) => ({ key: k, title: MONITORING_TITLES[k] ?? k, color: colorForKey(k) }))
      : [];
  } else {
    specs = props.kind === "vm" ? VM_KEYS : APP_KEYS;
  }

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
            const m = data?.metrics?.[key];
            return (
              <MetricChart
                key={key}
                title={title}
                unit={m?.unit ?? ""}
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

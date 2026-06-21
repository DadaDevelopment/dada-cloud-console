"use client";
import { useCallback, useEffect, useState } from "react";
import { appServersApi, appsApi, monitoringApi } from "@/lib/api";
import type { MetricsResponse } from "@/lib/types";
import { MetricChart } from "@/components/metric-chart";
import { Spinner } from "@/components/ui/spinner";

const RANGES = ["15m", "1h", "6h", "24h"] as const;
type Range = (typeof RANGES)[number];

// Display titles + draw order per metric key. Keys match the backend metric
// spec (metrics.go). Unknown keys still render with their raw key as title.
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
const MONITORING_KEYS: { key: string; title: string; color?: string }[] = [
  { key: "cpu", title: "CPU" },
  { key: "memory", title: "Memory", color: "#7c3aed" },
  { key: "temperature", title: "Temp", color: "#ea580c" },
];

export function MetricsPanel(
  props:
    | { kind: "vm"; projectId: string; serverName: string }
    | { kind: "app"; projectId: string; envId: string; appName: string }
    | { kind: "monitoring"; projectId: string; envId: string; appId: string }
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
      ? `monitoring:${props.projectId}:${props.envId}:${props.appId}`
      : `app:${props.projectId}:${props.envId}:${props.appName}`;

  const load = useCallback(async () => {
    try {
      let d: MetricsResponse;
      if (props.kind === "vm") {
        d = await appServersApi.getMetrics(props.projectId, props.serverName, range);
      } else if (props.kind === "monitoring") {
        d = await monitoringApi.getMetrics(props.projectId, props.envId, props.appId, range);
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

  // Build specs: for monitoring, start with known keys then append dynamic ones.
  let specs: { key: string; title: string; color?: string }[];
  if (props.kind === "monitoring") {
    const knownKeys = new Set(MONITORING_KEYS.map((k) => k.key));
    const dynamicKeys = data
      ? Object.keys(data.metrics).filter((k) => !knownKeys.has(k)).map((k) => ({ key: k, title: k }))
      : [];
    specs = [...MONITORING_KEYS, ...dynamicKeys];
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

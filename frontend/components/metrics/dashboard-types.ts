import type { VizType, Threshold } from "@/components/charts/types";

/** Refresh interval options (ms). 0 = paused. */
export const REFRESH_OPTIONS = [
  { label: "Off", value: 0 },
  { label: "10s", value: 10_000 },
  { label: "30s", value: 30_000 },
  { label: "1m", value: 60_000 },
  { label: "5m", value: 300_000 },
] as const;

/** Time-range presets understood by the backend's flexible parser. */
export const RANGE_PRESETS = ["15m", "1h", "6h", "24h", "7d"] as const;

/** Aggregations the backend now exposes via the ?agg param. */
export const AGG_OPTIONS = [
  { label: "Default", value: "" },
  { label: "Avg", value: "avg" },
  { label: "Sum", value: "sum" },
  { label: "Min", value: "min" },
  { label: "Max", value: "max" },
  { label: "Count", value: "count" },
  { label: "p50", value: "p50" },
  { label: "p90", value: "p90" },
  { label: "p95", value: "p95" },
  { label: "p99", value: "p99" },
] as const;

export interface PanelConfig {
  id: string;
  metric: string;
  viz: VizType;
  title?: string;
  /** Per-panel overrides; fall back to the dashboard globals when undefined. */
  groupBy?: string;
  agg?: string;
  thresholds?: Threshold[];
  /** react-grid-layout cell. */
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface DashboardState {
  version: number;
  range: string;
  from?: number;
  to?: number;
  refreshMs: number;
  groupBy: string;
  agg: string;
  filters: Record<string, string>;
  panels: PanelConfig[];
  /** false until the user has explicitly arranged panels; drives auto-seeding. */
  initialized: boolean;
}

export const DASHBOARD_VERSION = 1;

export function defaultDashboardState(): DashboardState {
  return {
    version: DASHBOARD_VERSION,
    range: "1h",
    refreshMs: 30_000,
    groupBy: "",
    agg: "",
    filters: {},
    panels: [],
    initialized: false,
  };
}

/** Resolves the dashboard time window into the API's range/from/to params. */
export function rangeParams(s: DashboardState): { range?: string; from?: number; to?: number } {
  if (s.range === "custom" && s.from && s.to) return { from: s.from, to: s.to };
  return { range: s.range };
}

/** True when the window spans more than a day (axis labels then show dates). */
export function isWideRange(s: DashboardState): boolean {
  if (s.range === "custom" && s.from && s.to) return s.to - s.from > 86_400;
  return s.range === "7d" || s.range === "24h";
}

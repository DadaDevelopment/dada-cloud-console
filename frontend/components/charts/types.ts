import type { MetricSeries } from "@/lib/types";

/** Every visualization the panel renderer can produce. */
export type VizType =
  | "line"
  | "area"
  | "stacked-area"
  | "bar"
  | "histogram"
  | "heatmap"
  | "scatter"
  | "gauge"
  | "sparkline"
  | "status-timeline";

export const VIZ_LABELS: Record<VizType, string> = {
  line: "Line",
  area: "Area",
  "stacked-area": "Stacked area",
  bar: "Bar",
  histogram: "Histogram",
  heatmap: "Heatmap",
  scatter: "Scatter",
  gauge: "Gauge",
  sparkline: "Sparkline",
  "status-timeline": "Status timeline",
};

/** A horizontal reference line drawn across a time-series chart. */
export interface Threshold {
  value: number;
  color: string;
  label?: string;
}

/** A vertical marker at a point in time (unix seconds). */
export interface Annotation {
  time: number;
  label: string;
  color?: string;
}

export interface BuildArgs {
  series: MetricSeries[];
  viz: VizType;
  unit: string;
  thresholds?: Threshold[];
  annotations?: Annotation[];
  /** True when the window spans more than a day, so axis labels show dates. */
  wideRange?: boolean;
  /** Enable the zoom/pan slider (off for compact panels). */
  zoom?: boolean;
}

export type { MetricSeries };

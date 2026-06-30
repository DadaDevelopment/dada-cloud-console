import type { EChartsOption } from "echarts";
import type { BuildArgs, Threshold, Annotation, VizType } from "./types";
import type { MetricSeries } from "@/lib/types";
import { colorForLabel, SERIES_PALETTE } from "./theme";
import { formatValue, formatTimeAxis } from "./format";

const PROGRESSIVE = { progressive: 4000, progressiveThreshold: 6000 } as const;

function thresholdMarkLine(thresholds: Threshold[] | undefined) {
  if (!thresholds?.length) return undefined;
  return {
    silent: true,
    symbol: "none" as const,
    lineStyle: { type: "dashed" as const, width: 1 },
    label: { position: "insideEndTop" as const, fontSize: 10 },
    data: thresholds.map((t) => ({
      yAxis: t.value,
      lineStyle: { color: t.color },
      label: { formatter: t.label ?? `${t.value}`, color: t.color },
    })),
  };
}

function annotationMarkLine(annotations: Annotation[] | undefined) {
  if (!annotations?.length) return undefined;
  return {
    silent: true,
    symbol: ["none", "none"] as [string, string],
    lineStyle: { type: "solid" as const, width: 1, opacity: 0.6 },
    label: { position: "insideEndTop" as const, fontSize: 10, rotate: 90 },
    data: annotations.map((a) => ({
      xAxis: a.time * 1000,
      lineStyle: { color: a.color ?? "#9ca3af" },
      label: { formatter: a.label, color: a.color ?? "#9ca3af" },
    })),
  };
}

/**
 * buildTimeSeries renders line / area / stacked-area / bar / scatter charts from
 * labelled metric series. Counters and gauges feed the same builder — kind only
 * affects upstream querying. Zoom/pan come from a dataZoom slider + inside drag;
 * legend toggling and the crosshair tooltip are inherited from the base option.
 */
export function buildTimeSeries(args: BuildArgs): EChartsOption {
  const { series, viz, unit, thresholds, annotations, wideRange, zoom } = args;
  const isBar = viz === "bar";
  const isScatter = viz === "scatter";
  const isArea = viz === "area" || viz === "stacked-area";
  const isStacked = viz === "stacked-area";

  const seriesDefs = series.map((s, i) => {
    const color = colorForLabel(s.label, i);
    const data = s.points.map((p) => [p.t * 1000, p.v] as [number, number]);
    if (isScatter) {
      return {
        name: s.label || "value",
        type: "scatter" as const,
        symbolSize: 5,
        itemStyle: { color, opacity: 0.7 },
        data,
        ...PROGRESSIVE,
      };
    }
    if (isBar) {
      return {
        name: s.label || "value",
        type: "bar" as const,
        stack: isStacked ? "total" : undefined,
        itemStyle: { color, borderRadius: [2, 2, 0, 0] },
        data,
        large: true,
        ...PROGRESSIVE,
      };
    }
    return {
      name: s.label || "value",
      type: "line" as const,
      stack: isStacked ? "total" : undefined,
      showSymbol: false,
      smooth: 0.2,
      lineStyle: { width: 1.75, color },
      itemStyle: { color },
      areaStyle: isArea
        ? { opacity: isStacked ? 0.5 : 0.12, color }
        : undefined,
      emphasis: { focus: "series" as const },
      data,
      sampling: "lttb" as const,
      ...PROGRESSIVE,
    };
  });

  // Thresholds/annotations are attached to the first series so they draw once.
  if (seriesDefs.length > 0) {
    const ml = thresholdMarkLine(thresholds);
    const al = annotationMarkLine(annotations);
    const markLineData = [...(ml?.data ?? []), ...(al?.data ?? [])];
    if (markLineData.length) {
      (seriesDefs[0] as Record<string, unknown>).markLine = {
        silent: true,
        symbol: "none",
        label: { fontSize: 10 },
        data: markLineData,
      };
    }
  }

  return {
    legend: { show: series.length > 1 },
    grid: { left: 8, right: 16, top: 16, bottom: series.length > 1 ? 48 : 28, containLabel: true },
    xAxis: {
      type: "time",
      axisLabel: {
        hideOverlap: true,
        formatter: (v: number) => formatTimeAxis(v, !!wideRange),
      },
      axisLine: { show: false },
      axisTick: { show: false },
    },
    yAxis: {
      type: "value",
      scale: viz !== "bar",
      axisLabel: { formatter: (v: number) => formatValue(v, unit) },
      splitLine: { lineStyle: { type: "dashed" } },
    },
    dataZoom: zoom
      ? [
          { type: "inside", throttle: 50 },
          { type: "slider", height: 18, bottom: series.length > 1 ? 26 : 4, borderColor: "transparent" },
        ]
      : [{ type: "inside", throttle: 50 }],
    tooltip: {
      valueFormatter: (v) => formatValue(Number(v), unit),
    },
    series: seriesDefs,
  };
}

/**
 * buildHistogram bins the latest value distribution across all points/series into
 * a frequency bar chart — useful for latency or size spreads.
 */
export function buildHistogram(series: MetricSeries[], unit: string, bins = 24): EChartsOption {
  const values = series.flatMap((s) => s.points.map((p) => p.v)).filter(isFinite);
  if (values.length === 0) return { series: [] };
  const min = Math.min(...values);
  const max = Math.max(...values);
  const width = (max - min) / bins || 1;
  const counts = new Array(bins).fill(0);
  for (const v of values) {
    const idx = Math.min(bins - 1, Math.floor((v - min) / width));
    counts[idx]++;
  }
  const cats = counts.map((_, i) => formatValue(min + i * width, unit));
  return {
    legend: { show: false },
    grid: { left: 8, right: 16, top: 16, bottom: 24, containLabel: true },
    xAxis: { type: "category", data: cats, axisLabel: { hideOverlap: true, fontSize: 10 }, axisTick: { show: false } },
    yAxis: { type: "value", splitLine: { lineStyle: { type: "dashed" } } },
    tooltip: { trigger: "axis" },
    series: [
      {
        type: "bar",
        data: counts,
        itemStyle: { color: SERIES_PALETTE[0], borderRadius: [2, 2, 0, 0] },
        barWidth: "92%",
      },
    ],
  };
}

/**
 * buildHeatmap lays series (y) against time buckets (x) with value-driven color,
 * surfacing hotspots across many group-by dimensions at once.
 */
export function buildHeatmap(series: MetricSeries[], unit: string, wideRange: boolean): EChartsOption {
  const times = Array.from(new Set(series.flatMap((s) => s.points.map((p) => p.t)))).sort((a, b) => a - b);
  const tIndex = new Map(times.map((t, i) => [t, i]));
  const rows = series.map((s) => s.label || "value");
  const data: [number, number, number][] = [];
  let vmin = Infinity;
  let vmax = -Infinity;
  series.forEach((s, y) => {
    for (const p of s.points) {
      const x = tIndex.get(p.t);
      if (x === undefined) continue;
      data.push([x, y, p.v]);
      if (p.v < vmin) vmin = p.v;
      if (p.v > vmax) vmax = p.v;
    }
  });
  if (!isFinite(vmin)) {
    vmin = 0;
    vmax = 1;
  }
  return {
    legend: { show: false },
    grid: { left: 8, right: 16, top: 16, bottom: 48, containLabel: true },
    xAxis: {
      type: "category",
      data: times.map((t) => formatTimeAxis(t * 1000, wideRange)),
      axisLabel: { hideOverlap: true, fontSize: 10 },
      axisTick: { show: false },
    },
    yAxis: { type: "category", data: rows, axisLabel: { fontSize: 10 } },
    visualMap: {
      min: vmin,
      max: vmax,
      calculable: true,
      orient: "horizontal",
      left: "center",
      bottom: 0,
      itemHeight: 80,
      textStyle: { fontSize: 10 },
      inRange: { color: ["#1e3a8a", "#3b82f6", "#22d3ee", "#facc15", "#ef4444"] },
    },
    tooltip: {
      position: "top",
      formatter: (p: unknown) => {
        const d = p as { value: [number, number, number] };
        return `${rows[d.value[1]]}: ${formatValue(d.value[2], unit)}`;
      },
    },
    series: [
      {
        type: "heatmap",
        data,
        progressive: 4000,
        emphasis: { itemStyle: { borderColor: "#fff", borderWidth: 1 } },
      },
    ],
  };
}

/**
 * buildGauge shows a single current value against a max with threshold-colored
 * arc segments — the dial form for a KPI panel.
 */
export function buildGauge(
  value: number,
  unit: string,
  max: number,
  thresholds?: Threshold[],
): EChartsOption {
  const segs: [number, string][] =
    thresholds && thresholds.length
      ? [...thresholds]
          .sort((a, b) => a.value - b.value)
          .map((t) => [Math.min(1, t.value / (max || 1)), t.color] as [number, string])
          .concat([[1, "#e5e7eb"]])
      : [
          [0.6, SERIES_PALETTE[3]],
          [0.85, SERIES_PALETTE[6]],
          [1, SERIES_PALETTE[7]],
        ];
  return {
    legend: { show: false },
    tooltip: { show: false },
    series: [
      {
        type: "gauge",
        min: 0,
        max,
        radius: "92%",
        center: ["50%", "58%"],
        progress: { show: false },
        axisLine: { lineStyle: { width: 12, color: segs } },
        pointer: { length: "60%", width: 4, itemStyle: { color: "auto" } },
        axisTick: { show: false },
        splitLine: { length: 10, lineStyle: { width: 1, color: "#cbd5e1" } },
        axisLabel: { distance: 14, fontSize: 9, formatter: (v: number) => formatValue(v, unit) },
        detail: {
          valueAnimation: false,
          fontSize: 22,
          offsetCenter: [0, "40%"],
          formatter: () => formatValue(value, unit),
          color: "auto",
        },
        data: [{ value }],
      },
    ],
  };
}

/**
 * buildSparkline is the minimal inline trend used in KPI cards and the explorer
 * list — no axes, no tooltip, single tinted area line.
 */
export function buildSparkline(series: MetricSeries[], colorIndex = 0): EChartsOption {
  const pts = series[0]?.points ?? [];
  const color = SERIES_PALETTE[colorIndex % SERIES_PALETTE.length];
  return {
    backgroundColor: "transparent",
    animation: false,
    grid: { left: 1, right: 1, top: 2, bottom: 1 },
    xAxis: { type: "time", show: false },
    yAxis: { type: "value", show: false, scale: true },
    tooltip: { show: false },
    legend: { show: false },
    series: [
      {
        type: "line",
        showSymbol: false,
        smooth: 0.3,
        lineStyle: { width: 1.5, color },
        areaStyle: { opacity: 0.15, color },
        data: pts.map((p) => [p.t * 1000, p.v]),
        sampling: "lttb",
      },
    ],
  };
}

/**
 * buildStatusTimeline maps a single series to colored time bands via thresholds,
 * giving an at-a-glance uptime/health strip. Bands derive from the first matching
 * threshold; unmatched samples render in the ok color.
 */
export function buildStatusTimeline(
  series: MetricSeries[],
  thresholds: Threshold[],
  wideRange: boolean,
): EChartsOption {
  const pts = series[0]?.points ?? [];
  const sorted = [...thresholds].sort((a, b) => b.value - a.value);
  const colorAt = (v: number) => sorted.find((t) => v >= t.value)?.color ?? SERIES_PALETTE[3];
  const data = pts.map((p) => ({ value: [p.t * 1000, 1], itemStyle: { color: colorAt(p.v) } }));
  return {
    legend: { show: false },
    grid: { left: 8, right: 16, top: 8, bottom: 24, containLabel: true },
    xAxis: {
      type: "time",
      axisLabel: { hideOverlap: true, formatter: (v: number) => formatTimeAxis(v, wideRange) },
      axisTick: { show: false },
    },
    yAxis: { type: "value", show: false, max: 1 },
    tooltip: { trigger: "axis", show: true },
    series: [
      {
        type: "bar",
        data,
        barWidth: "100%",
        large: true,
      },
    ],
  };
}

/** dispatchBuild routes a panel's viz type + data to the right builder. */
export function dispatchBuild(args: BuildArgs): EChartsOption {
  switch (args.viz) {
    case "histogram":
      return buildHistogram(args.series, args.unit);
    case "heatmap":
      return buildHeatmap(args.series, args.unit, !!args.wideRange);
    case "scatter":
      return buildTimeSeries(args);
    case "status-timeline":
      return buildStatusTimeline(args.series, args.thresholds ?? [], !!args.wideRange);
    default:
      return buildTimeSeries(args);
  }
}

export type { VizType };

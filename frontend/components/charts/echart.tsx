"use client";
import { useEffect, useRef, useState } from "react";
import * as echarts from "echarts/core";
import { LineChart, BarChart, ScatterChart, GaugeChart, HeatmapChart } from "echarts/charts";
import {
  GridComponent,
  TooltipComponent,
  LegendComponent,
  DataZoomComponent,
  DataZoomInsideComponent,
  DataZoomSliderComponent,
  VisualMapComponent,
  MarkLineComponent,
} from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";
import type { EChartsOption, EChartsType } from "echarts";
import { cn } from "@/lib/cn";
import { THEME_TOKENS, type ChartTheme } from "./theme";

// Tree-shaken registration: only the charts/components the builders actually use
// are pulled in, so the route chunk ships a fraction of the full echarts bundle.
// LineChart carries the lttb sampling + progressive rendering the builders rely on.
echarts.use([
  LineChart,
  BarChart,
  ScatterChart,
  GaugeChart,
  HeatmapChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  DataZoomComponent,
  DataZoomInsideComponent,
  DataZoomSliderComponent,
  VisualMapComponent,
  MarkLineComponent,
  CanvasRenderer,
]);

/**
 * useChartTheme tracks the console's `.dark` class on <html> so charts re-render
 * with a matching palette when the user flips the in-app theme toggle. A
 * MutationObserver on the class attribute drives the update. Returns "light" on
 * the server and before hydration to keep SSR markup stable.
 */
export function useChartTheme(): ChartTheme {
  const [theme, setTheme] = useState<ChartTheme>("light");
  useEffect(() => {
    const root = document.documentElement;
    const apply = () => setTheme(root.classList.contains("dark") ? "dark" : "light");
    apply();
    const obs = new MutationObserver(apply);
    obs.observe(root, { attributes: true, attributeFilter: ["class"] });
    return () => obs.disconnect();
  }, []);
  return theme;
}

/**
 * baseOption supplies the chrome every chart shares — grid insets, a crosshair
 * tooltip, theme-aware axis styling and a transparent background. Builders merge
 * their series/axis specifics on top via deepMerge in setOption(notMerge).
 */
function baseOption(theme: ChartTheme): EChartsOption {
  const t = THEME_TOKENS[theme];
  return {
    backgroundColor: "transparent",
    animation: false,
    textStyle: { fontFamily: "inherit", color: t.text },
    grid: { left: 8, right: 16, top: 28, bottom: 8, containLabel: true },
    tooltip: {
      trigger: "axis",
      axisPointer: { type: "cross", lineStyle: { color: t.muted, type: "dashed" } },
      backgroundColor: t.tooltipBg,
      borderColor: t.tooltipBorder,
      borderWidth: 1,
      padding: [8, 12],
      textStyle: { color: t.tooltipText, fontSize: 12 },
      extraCssText: "box-shadow: 0 8px 24px rgba(0,0,0,0.12); border-radius: 8px;",
    },
    legend: {
      type: "scroll",
      bottom: 0,
      icon: "roundRect",
      itemWidth: 10,
      itemHeight: 10,
      textStyle: { color: t.axisLabel, fontSize: 11 },
      inactiveColor: t.muted,
    },
  };
}

export interface EChartProps {
  option: EChartsOption;
  /** Pixel height of the chart canvas. Width is always fluid. */
  height?: number | string;
  className?: string;
  /** Echarts event name → handler, e.g. { datazoom: fn }. */
  onEvents?: Record<string, (params: unknown) => void>;
  /** Connect group id so multiple charts share zoom/tooltip. */
  group?: string;
  /** Disable the shared chrome (rarely needed; gauges/sparklines override). */
  bare?: boolean;
  "aria-label"?: string;
}

/**
 * EChart is the single ECharts adapter for the dashboard. It owns instance
 * lifecycle, resizes via ResizeObserver (not window events, so grid-layout panel
 * resizes are honored), and re-inits when the OS theme flips. Builders produce
 * the option; this component only renders and keeps it sized.
 */
export function EChart({
  option,
  height = 240,
  className,
  onEvents,
  group,
  bare,
  ...rest
}: EChartProps) {
  const elRef = useRef<HTMLDivElement | null>(null);
  const chartRef = useRef<EChartsType | null>(null);
  const theme = useChartTheme();

  useEffect(() => {
    if (!elRef.current) return;
    const chart = echarts.init(elRef.current, undefined, { renderer: "canvas" });
    chartRef.current = chart;
    if (group) {
      chart.group = group;
      echarts.connect(group);
    }
    const ro = new ResizeObserver(() => chart.resize());
    ro.observe(elRef.current);
    return () => {
      ro.disconnect();
      chart.dispose();
      chartRef.current = null;
    };
    // Re-init on theme flip so the merged base option's colors are reapplied.
  }, [theme, group]);

  useEffect(() => {
    const chart = chartRef.current;
    if (!chart) return;
    const merged = bare ? option : mergeOption(baseOption(theme), option);
    chart.setOption(merged, { notMerge: true, lazyUpdate: true });
  }, [option, theme, bare]);

  useEffect(() => {
    const chart = chartRef.current;
    if (!chart || !onEvents) return;
    for (const [evt, fn] of Object.entries(onEvents)) chart.on(evt, fn);
    return () => {
      for (const evt of Object.keys(onEvents)) chart.off(evt);
    };
  }, [onEvents]);

  return (
    <div
      ref={elRef}
      role="img"
      aria-label={rest["aria-label"]}
      className={cn("w-full", className)}
      style={{ height }}
    />
  );
}

/**
 * mergeOption shallow-merges builder option over the shared base, deep-merging
 * the few nested objects (grid/tooltip/legend/textStyle) so a builder can tweak
 * one tooltip field without dropping the base crosshair config.
 */
function mergeOption(base: EChartsOption, over: EChartsOption): EChartsOption {
  const out: Record<string, unknown> = { ...base };
  for (const [k, v] of Object.entries(over)) {
    const bv = (base as Record<string, unknown>)[k];
    if (
      v &&
      bv &&
      typeof v === "object" &&
      typeof bv === "object" &&
      !Array.isArray(v) &&
      !Array.isArray(bv)
    ) {
      out[k] = { ...(bv as object), ...(v as object) };
    } else {
      out[k] = v;
    }
  }
  return out as EChartsOption;
}

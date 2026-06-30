"use client";
import { useMemo } from "react";
import type { MetricSeries } from "@/lib/types";

// formatValue renders a metric value with a unit-appropriate, human-readable
// suffix. Units come from the backend metric spec ("%", "B", "B/s", "cores").
export function formatValue(v: number, unit: string): string {
  if (!isFinite(v)) return "—";
  if (unit === "%") return `${v.toFixed(1)}%`;
  if (unit === "cores") return v.toFixed(3);
  if (unit === "B" || unit === "B/s") {
    const suffix = unit === "B/s" ? "/s" : "";
    const units = ["B", "KB", "MB", "GB", "TB"];
    let n = v;
    let i = 0;
    while (n >= 1024 && i < units.length - 1) {
      n /= 1024;
      i++;
    }
    return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}${suffix}`;
  }
  return v.toFixed(2);
}

// SERIES_PALETTE assigns a stable color per series index when a metric is split
// into multiple group-by series.
const SERIES_PALETTE = [
  "#2563eb",
  "#7c3aed",
  "#ea580c",
  "#059669",
  "#0891b2",
  "#db2777",
  "#ca8a04",
  "#dc2626",
];

// MetricChart renders one or more dependency-free inline-SVG sparklines, one
// polyline per series, sharing a global value/time scale. Width is fluid
// (viewBox-based); height is fixed in px.
export function MetricChart({
  title,
  unit,
  series,
  color = "#2563eb",
  height = 64,
}: {
  title: string;
  unit: string;
  series: MetricSeries[];
  color?: string;
  kind?: "counter" | "gauge";
  height?: number;
}) {
  const view = useMemo(() => {
    const lines = series
      .map((s) => s.points.filter((p) => isFinite(p.v)))
      .filter((pts) => pts.length > 0);
    if (lines.length === 0) return null;

    const W = 100;
    const H = 40;
    const allVals = lines.flatMap((pts) => pts.map((p) => p.v));
    let min = Math.min(...allVals);
    let max = Math.max(...allVals);
    if (unit === "%") {
      min = 0;
      max = Math.max(max, 100);
    }
    if (max - min < 1e-9) max = min + 1; // flat-line guard

    const allTs = lines.flatMap((pts) => pts.map((p) => p.t));
    const tMin = Math.min(...allTs);
    const tMax = Math.max(...allTs);
    const tSpan = tMax - tMin || 1;
    const x = (t: number) => ((t - tMin) / tSpan) * W;
    const y = (v: number) => H - ((v - min) / (max - min)) * H;

    const single = series.length === 1 && (series[0]?.label ?? "") === "";
    const rendered = lines.map((pts, i) => {
      const c = single ? color : SERIES_PALETTE[i % SERIES_PALETTE.length];
      // A single-point series still gets a short flat segment + a dot so 1-2
      // point counters do not render as an invisible polyline.
      const poly =
        pts.length === 1
          ? `0,${y(pts[0].v).toFixed(2)} ${W},${y(pts[0].v).toFixed(2)}`
          : pts.map((p) => `${x(p.t).toFixed(2)},${y(p.v).toFixed(2)}`).join(" ");
      const dot = pts.length === 1 ? { cx: x(pts[0].t), cy: y(pts[0].v) } : null;
      return { color: c, poly, dot, label: series[i]?.label ?? "" };
    });

    const primary = lines[0];
    const current = primary[primary.length - 1].v;
    const baseline = y(min + (max - min) / 2);
    return { W, H, rendered, current, min, max, baseline, multi: series.length > 1 };
  }, [series, unit, color]);

  return (
    <div className="rounded-lg border border-gray-200 bg-white p-4">
      <div className="flex items-baseline justify-between">
        <span className="text-xs font-semibold uppercase tracking-wide text-gray-400">{title}</span>
        <span className="text-sm font-semibold text-gray-900">
          {view ? formatValue(view.current, unit) : "—"}
        </span>
      </div>
      <div className="mt-2" style={{ height }}>
        {view ? (
          <svg
            viewBox={`0 0 ${view.W} ${view.H}`}
            preserveAspectRatio="none"
            className="h-full w-full"
          >
            <line
              x1={0}
              x2={view.W}
              y1={view.baseline}
              y2={view.baseline}
              stroke="#e5e7eb"
              strokeWidth={0.5}
              vectorEffect="non-scaling-stroke"
            />
            {view.rendered.map((r, i) => (
              <g key={i}>
                <polyline
                  points={r.poly}
                  fill="none"
                  stroke={r.color}
                  strokeWidth={1.2}
                  vectorEffect="non-scaling-stroke"
                  strokeLinejoin="round"
                />
                {r.dot && (
                  <circle cx={r.dot.cx} cy={r.dot.cy} r={1.6} fill={r.color} />
                )}
              </g>
            ))}
          </svg>
        ) : (
          <div className="flex h-full items-center justify-center text-xs text-gray-300">
            no data
          </div>
        )}
      </div>
      {view?.multi && (
        <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1">
          {view.rendered.map((r, i) => (
            <span key={i} className="inline-flex items-center gap-1 text-[10px] text-gray-500">
              <span
                className="inline-block h-2 w-2 rounded-sm"
                style={{ backgroundColor: r.color }}
              />
              {r.label || "—"}
            </span>
          ))}
        </div>
      )}
      {view && (
        <div className="mt-1 flex justify-between text-[10px] text-gray-400">
          <span>min {formatValue(view.min, unit)}</span>
          <span>max {formatValue(view.max, unit)}</span>
        </div>
      )}
    </div>
  );
}

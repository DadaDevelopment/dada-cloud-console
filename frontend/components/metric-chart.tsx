"use client";
import { useMemo } from "react";
import type { MetricPoint } from "@/lib/types";

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

// MetricChart renders a single dependency-free inline-SVG area/sparkline.
// Width is fluid (viewBox-based); height is fixed in px.
export function MetricChart({
  title,
  unit,
  series,
  color = "#2563eb",
  height = 64,
}: {
  title: string;
  unit: string;
  series: MetricPoint[];
  color?: string;
  height?: number;
}) {
  const view = useMemo(() => {
    const pts = series.filter((p) => isFinite(p.v));
    if (pts.length === 0) return null;
    const W = 100;
    const H = 40;
    const vals = pts.map((p) => p.v);
    let min = Math.min(...vals);
    let max = Math.max(...vals);
    if (unit === "%") {
      min = 0;
      max = Math.max(max, 100);
    }
    if (max - min < 1e-9) max = min + 1; // flat line guard
    const ts = pts.map((p) => p.t);
    const tMin = Math.min(...ts);
    const tMax = Math.max(...ts);
    const tSpan = tMax - tMin || 1;
    const x = (t: number) => ((t - tMin) / tSpan) * W;
    const y = (v: number) => H - ((v - min) / (max - min)) * H;
    const line = pts.map((p) => `${x(p.t).toFixed(2)},${y(p.v).toFixed(2)}`).join(" ");
    const area = `0,${H} ${line} ${W},${H}`;
    return { W, H, line, area, current: pts[pts.length - 1].v, min, max };
  }, [series, unit]);

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
            <polyline points={view.area} fill={color} fillOpacity={0.1} stroke="none" />
            <polyline
              points={view.line}
              fill="none"
              stroke={color}
              strokeWidth={1.2}
              vectorEffect="non-scaling-stroke"
              strokeLinejoin="round"
            />
          </svg>
        ) : (
          <div className="flex h-full items-center justify-center text-xs text-gray-300">
            no data
          </div>
        )}
      </div>
      {view && (
        <div className="mt-1 flex justify-between text-[10px] text-gray-400">
          <span>min {formatValue(view.min, unit)}</span>
          <span>max {formatValue(view.max, unit)}</span>
        </div>
      )}
    </div>
  );
}

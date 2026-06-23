"use client";
import { useEffect, useState } from "react";
import { appsApi } from "@/lib/api";
import type { MetricPoint, MetricsResponse } from "@/lib/types";

// Compact CPU+RAM sparkline for an app card. Both series are normalized to their
// own max over the window and drawn in one mini SVG (shape comparison), with the
// current absolute values printed below. k8s apps only — for VM/compose the same
// endpoint returns compose-labelled series, which also render fine.

const CPU_COLOR = "#2563eb"; // blue
const MEM_COLOR = "#7c3aed"; // purple

function fmtCores(v: number): string {
  if (v >= 1) return `${v.toFixed(2)} cores`;
  return `${(v * 1000).toFixed(0)}m`; // millicores
}

function fmtBytes(v: number): string {
  if (v >= 1 << 30) return `${(v / (1 << 30)).toFixed(1)} GB`;
  if (v >= 1 << 20) return `${(v / (1 << 20)).toFixed(0)} MB`;
  if (v >= 1 << 10) return `${(v / (1 << 10)).toFixed(0)} KB`;
  return `${v.toFixed(0)} B`;
}

// polyline points string, normalized to [0,1] by series max, mapped into the box.
function path(points: MetricPoint[], w: number, h: number, pad: number): string {
  if (points.length === 0) return "";
  const max = Math.max(...points.map((p) => p.v), 0);
  const denom = max > 0 ? max : 1;
  const n = points.length;
  const innerH = h - pad * 2;
  return points
    .map((p, i) => {
      const x = n === 1 ? w : (i / (n - 1)) * w;
      const y = pad + innerH * (1 - p.v / denom);
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
}

function last(points: MetricPoint[]): number {
  return points.length ? points[points.length - 1].v : 0;
}

export function AppSparkline({
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

  useEffect(() => {
    let alive = true;
    appsApi
      .getMetrics(projectId, envId, appName, "1h")
      .then((d) => alive && setData(d))
      .catch(() => alive && setFailed(true));
    return () => {
      alive = false;
    };
  }, [projectId, envId, appName]);

  if (failed) return null;

  const cpu = data?.metrics?.cpu_cores?.series ?? [];
  const mem = data?.metrics?.mem_bytes?.series ?? [];
  const hasData = cpu.length > 0 || mem.length > 0;

  const W = 220;
  const H = 36;
  const PAD = 3;

  return (
    <div className="mt-3 border-t border-gray-100 pt-2">
      {!data ? (
        <div className="h-[36px] animate-pulse rounded bg-gray-50" />
      ) : !hasData ? (
        <p className="text-[11px] text-gray-300">No metrics</p>
      ) : (
        <>
          <svg
            viewBox={`0 0 ${W} ${H}`}
            preserveAspectRatio="none"
            className="h-9 w-full"
            aria-label="CPU and memory, last hour (normalized)"
          >
            {mem.length > 0 && (
              <polyline
                points={path(mem, W, H, PAD)}
                fill="none"
                stroke={MEM_COLOR}
                strokeWidth={1.5}
                vectorEffect="non-scaling-stroke"
              />
            )}
            {cpu.length > 0 && (
              <polyline
                points={path(cpu, W, H, PAD)}
                fill="none"
                stroke={CPU_COLOR}
                strokeWidth={1.5}
                vectorEffect="non-scaling-stroke"
              />
            )}
          </svg>
          <div className="mt-1 flex items-center gap-3 text-[11px]">
            <span className="flex items-center gap-1 text-gray-500">
              <span className="inline-block h-2 w-2 rounded-full" style={{ background: CPU_COLOR }} />
              CPU {fmtCores(last(cpu))}
            </span>
            <span className="flex items-center gap-1 text-gray-500">
              <span className="inline-block h-2 w-2 rounded-full" style={{ background: MEM_COLOR }} />
              RAM {fmtBytes(last(mem))}
            </span>
          </div>
        </>
      )}
    </div>
  );
}

"use client";
import { useMemo, useState } from "react";
import type { AdminFunnelChannel } from "@/lib/types";

const STAGE_KEYS = ["visits", "register_opened", "signup_started", "registration_complete", "deploy_success"] as const;
type StageKey = (typeof STAGE_KEYS)[number];

const SOURCE_COLORS: Record<string, string> = {
  "Direct traffic": "#3b82f6",
  "Internal traffic": "#10b981",
  "Search engine traffic": "#f59e0b",
  "Link traffic": "#8b5cf6",
  "Messenger traffic": "#ec4899",
  "Recommendation system traffic": "#06b6d4",
};
const FALLBACK_COLOR = "#94a3b8";

const WIDTH = 900;
const HEIGHT = 380;
const PAD_TOP = 34;
const PAD_BOTTOM = 8;
const NODE_W = 10;
const PLOT_H = HEIGHT - PAD_TOP - PAD_BOTTOM;

function ribbonPath(x0: number, y0top: number, y0bot: number, x1: number, y1top: number, y1bot: number): string {
  const xm = (x0 + x1) / 2;
  return [
    `M${x0},${y0top}`,
    `C${xm},${y0top} ${xm},${y1top} ${x1},${y1top}`,
    `L${x1},${y1bot}`,
    `C${xm},${y1bot} ${xm},${y0bot} ${x0},${y0bot}`,
    "Z",
  ].join(" ");
}

export function ChannelFunnelSankey({
  channels,
  totals,
  sourceLabel,
  stageLabels,
}: {
  channels: AdminFunnelChannel[];
  totals: AdminFunnelChannel;
  sourceLabel: (source: string) => string;
  stageLabels: string[];
}) {
  const [hovered, setHovered] = useState<string | null>(null);
  const [tooltip, setTooltip] = useState<{ x: number; y: number; source: string; stage: string; count: number } | null>(null);

  const ordered = useMemo(() => [...channels].sort((a, b) => b.visits - a.visits), [channels]);

  const maxTotal = Math.max(1, ...STAGE_KEYS.map((k) => totals[k as StageKey]));
  const scale = PLOT_H / maxTotal;
  const colX = STAGE_KEYS.map((_, i) => NODE_W / 2 + (i * (WIDTH - NODE_W)) / (STAGE_KEYS.length - 1));

  const bands = useMemo(() => {
    return STAGE_KEYS.map((key) => {
      let y = PAD_TOP;
      return ordered.map((c) => {
        const h = c[key as StageKey] * scale;
        const band = { source: c.source, y0: y, y1: y + h };
        y += h;
        return band;
      });
    });
  }, [ordered, scale]);

  if (ordered.length === 0) return null;

  return (
    <div className="relative">
      <svg viewBox={`0 0 ${WIDTH} ${HEIGHT}`} className="w-full" role="img" aria-label="Sankey-диаграмма канальной воронки">
        {STAGE_KEYS.map((key, i) => (
          <text
            key={key}
            x={colX[i]}
            y={16}
            textAnchor={i === 0 ? "start" : i === STAGE_KEYS.length - 1 ? "end" : "middle"}
            className="fill-gray-500 dark:fill-gray-400"
            style={{ fontSize: 11, fontWeight: 600 }}
          >
            {stageLabels[i]} · {totals[key as StageKey]}
          </text>
        ))}

        {STAGE_KEYS.slice(0, -1).map((_, si) =>
          ordered.map((c, ci) => {
            const b0 = bands[si][ci];
            const b1 = bands[si + 1][ci];
            const color = SOURCE_COLORS[c.source] ?? FALLBACK_COLOR;
            const dim = hovered && hovered !== c.source;
            return (
              <path
                key={`${c.source}-${si}`}
                d={ribbonPath(colX[si] + NODE_W / 2, b0.y0, b0.y1, colX[si + 1] - NODE_W / 2, b1.y0, b1.y1)}
                fill={color}
                opacity={dim ? 0.12 : 0.55}
                onMouseEnter={() => setHovered(c.source)}
                onMouseLeave={() => {
                  setHovered(null);
                  setTooltip(null);
                }}
                onMouseMove={(e) => {
                  const rect = (e.target as SVGElement).ownerSVGElement?.getBoundingClientRect();
                  if (!rect) return;
                  setTooltip({
                    x: e.clientX - rect.left,
                    y: e.clientY - rect.top,
                    source: sourceLabel(c.source),
                    stage: stageLabels[si],
                    count: c[STAGE_KEYS[si] as StageKey],
                  });
                }}
              />
            );
          }),
        )}

        {STAGE_KEYS.map((key, si) =>
          ordered.map((c, ci) => {
            const b = bands[si][ci];
            if (b.y1 <= b.y0) return null;
            const color = SOURCE_COLORS[c.source] ?? FALLBACK_COLOR;
            const dim = hovered && hovered !== c.source;
            return (
              <rect
                key={`node-${key}-${c.source}`}
                x={colX[si] - NODE_W / 2}
                y={b.y0}
                width={NODE_W}
                height={Math.max(1, b.y1 - b.y0)}
                fill={color}
                opacity={dim ? 0.3 : 1}
                rx={2}
              />
            );
          }),
        )}
      </svg>

      {tooltip && (
        <div
          className="pointer-events-none absolute z-10 rounded-md border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 px-2.5 py-1.5 text-xs shadow-lg"
          style={{ left: tooltip.x + 12, top: tooltip.y + 12 }}
        >
          <div className="font-semibold text-gray-900 dark:text-gray-100">{tooltip.source}</div>
          <div className="text-gray-500 dark:text-gray-400">
            {tooltip.stage}: <span className="font-medium tabular-nums text-gray-900 dark:text-gray-100">{tooltip.count}</span>
          </div>
        </div>
      )}

      <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1.5">
        {ordered.map((c) => (
          <button
            key={c.source}
            type="button"
            onMouseEnter={() => setHovered(c.source)}
            onMouseLeave={() => setHovered(null)}
            className="flex items-center gap-1.5 text-xs text-gray-600 dark:text-gray-300"
            style={{ opacity: hovered && hovered !== c.source ? 0.4 : 1 }}
          >
            <span className="h-2.5 w-2.5 shrink-0 rounded-sm" style={{ backgroundColor: SOURCE_COLORS[c.source] ?? FALLBACK_COLOR }} />
            {sourceLabel(c.source)}
          </button>
        ))}
      </div>
    </div>
  );
}

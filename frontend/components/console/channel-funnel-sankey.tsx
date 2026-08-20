"use client";
import { useMemo, useState } from "react";
import { buildChannelFunnelLayout, type ChannelFunnelSeries } from "@/lib/channel-funnel";

const SOURCE_COLORS: Record<string, string> = {
  "Direct traffic": "#3b82f6",
  "Internal traffic": "#f97316",
  "Search engine traffic": "#10b981",
  "Link traffic": "#a855f7",
  "Messenger traffic": "#ec4899",
  "Social network traffic": "#06b6d4",
  "Recommendation system traffic": "#14b8a6",
  "Ad traffic": "#eab308",
};
const FALLBACK_COLORS = ["#6366f1", "#0ea5e9", "#84cc16", "#f43f5e", "#8b5cf6"];
const DROP_COLOR = "#cbd5e1";
const DROP_COLOR_DARK = "#475569";

/**
 * Text sits on top of the ribbons it describes; the halo is what keeps a
 * number legible where a wide band runs underneath it. `currentColor` picks up
 * the card background from the wrapper, so it inverts with the theme.
 */
const HALO = {
  paintOrder: "stroke" as const,
  stroke: "currentColor",
  strokeWidth: 3,
  strokeLinejoin: "round" as const,
};

const TOP_PAD = 26;
const BOTTOM_PAD = 6;

function colorFor(source: string, index: number): string {
  return SOURCE_COLORS[source] ?? FALLBACK_COLORS[index % FALLBACK_COLORS.length];
}

/**
 * Channel funnel as a drop-off Sankey: one ribbon per traffic source, split at
 * every stage into the people who went on and the people who left. Colour
 * follows the source the whole way, so loss stays attributable to the channel
 * that delivered it.
 *
 * Stage values must be unique users (Metrika ym:s:goal<id>users). Feeding it
 * raw goal reaches makes later stages exceed earlier ones and the diagram
 * silently becomes a lie about growth.
 */
export function ChannelFunnelSankey({
  channels,
  sourceLabel,
  stageLabels,
  dropLabels,
  clampNote,
}: {
  channels: ChannelFunnelSeries[];
  sourceLabel: (source: string) => string;
  stageLabels: string[];
  dropLabels: string[];
  clampNote: (sources: string) => string;
}) {
  const [hovered, setHovered] = useState<string | null>(null);

  const layout = useMemo(
    () => buildChannelFunnelLayout(channels, stageLabels, dropLabels),
    [channels, stageLabels, dropLabels],
  );

  const colors = useMemo(() => {
    const map = new Map<string, string>();
    layout.nodes
      .filter((n) => n.kind === "source")
      .forEach((n, i) => map.set(n.source!, colorFor(n.source!, i)));
    return map;
  }, [layout]);

  const nodeLabels = useMemo(() => new Map(layout.nodes.map((n) => [n.id, n.label])), [layout]);

  if (layout.nodes.length === 0) return null;

  const viewH = layout.height + TOP_PAD + BOTTOM_PAD;
  const columnCount = stageLabels.length;
  const columnX = (column: number) => (column * (layout.width - layout.nodeWidth)) / (columnCount - 1);

  return (
    <div className="text-white dark:text-gray-900">
      <svg
        viewBox={`0 0 ${layout.width} ${viewH}`}
        className="w-full max-w-[860px]"
        role="img"
        aria-label="Канальная воронка: источник → регистрация"
      >
        <g transform={`translate(0 ${TOP_PAD})`}>
          <text
            x={columnX(0)}
            y={-12}
            className="fill-gray-500 dark:fill-gray-400"
            style={{ fontSize: 11, fontWeight: 600 }}
          >
            {stageLabels[0]} · {layout.entryTotal}
          </text>

          {layout.links.map((link) => {
            const xm = (link.x0 + link.x1) / 2;
            const dim = hovered !== null && hovered !== link.source;
            const color = link.kind === "drop" ? DROP_COLOR : (colors.get(link.source) ?? FALLBACK_COLORS[0]);
            return (
              <path
                key={link.id}
                d={`M${link.x0},${link.y0} C${xm},${link.y0} ${xm},${link.y1} ${link.x1},${link.y1}`}
                fill="none"
                stroke={color}
                strokeWidth={link.width}
                opacity={dim ? 0.08 : link.kind === "drop" ? 0.32 : 0.62}
                className={link.kind === "drop" ? "dark:stroke-slate-600" : undefined}
                onMouseEnter={() => setHovered(link.source)}
                onMouseLeave={() => setHovered(null)}
              >
                <title>{`${sourceLabel(link.source)} → ${nodeLabels.get(link.target) ?? ""}: ${link.value}`}</title>
              </path>
            );
          })}

          {layout.nodes.map((node) => {
            const isSource = node.kind === "source";
            const dim = hovered !== null && isSource && hovered !== node.source;
            const fill = isSource
              ? (colors.get(node.source!) ?? FALLBACK_COLORS[0])
              : node.kind === "drop"
                ? DROP_COLOR
                : "#1f2937";
            const last = node.column === columnCount - 1;
            const labelX = last ? node.x - 8 : node.x + layout.nodeWidth + 8;
            const anchor = last ? "end" : "start";
            const showLabel = !isSource && node.value > 0;
            return (
              <g key={node.id}>
                <rect
                  x={node.x}
                  y={node.y}
                  width={layout.nodeWidth}
                  height={Math.max(node.height, 1)}
                  rx={2}
                  fill={fill}
                  opacity={dim ? 0.25 : 1}
                  className={node.kind === "drop" ? "dark:fill-slate-600" : node.kind === "passed" ? "dark:fill-gray-100" : undefined}
                  onMouseEnter={() => isSource && setHovered(node.source!)}
                  onMouseLeave={() => setHovered(null)}
                >
                  <title>{`${isSource ? sourceLabel(node.source!) : node.label}: ${node.value}`}</title>
                </rect>
                {showLabel && (
                  <>
                    <text
                      x={labelX}
                      y={node.y + Math.max(node.height, 12) / 2 - 2}
                      textAnchor={anchor}
                      className={node.kind === "drop" ? "fill-gray-400 dark:fill-gray-500" : "fill-gray-900 dark:fill-gray-100"}
                      style={{ ...HALO, fontSize: 11, fontWeight: node.kind === "passed" ? 600 : 500 }}
                    >
                      {node.label}
                    </text>
                    <text
                      x={labelX}
                      y={node.y + Math.max(node.height, 12) / 2 + 12}
                      textAnchor={anchor}
                      className={node.kind === "drop" ? "fill-gray-400 dark:fill-gray-500" : "fill-blue-600 dark:fill-blue-400"}
                      style={{ ...HALO, fontSize: 11, fontWeight: 700 }}
                    >
                      {node.value}
                      {node.share !== undefined ? ` · ${(node.share * 100).toFixed(1)}%` : ""}
                    </text>
                  </>
                )}
              </g>
            );
          })}
        </g>
      </svg>

      <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1.5">
        {layout.nodes
          .filter((n) => n.kind === "source")
          .map((n) => (
            <button
              key={n.id}
              type="button"
              onMouseEnter={() => setHovered(n.source!)}
              onMouseLeave={() => setHovered(null)}
              className="flex items-center gap-1.5 text-xs text-gray-600 dark:text-gray-300"
              style={{ opacity: hovered !== null && hovered !== n.source ? 0.4 : 1 }}
            >
              <span
                className="h-2.5 w-2.5 shrink-0 rounded-sm"
                style={{ backgroundColor: colors.get(n.source!) ?? FALLBACK_COLORS[0] }}
              />
              {sourceLabel(n.source!)} · {n.value}
            </button>
          ))}
        <span className="flex items-center gap-1.5 text-xs text-gray-400 dark:text-gray-500">
          <span className="h-2.5 w-2.5 shrink-0 rounded-sm" style={{ backgroundColor: DROP_COLOR_DARK, opacity: 0.5 }} />
          {dropLabels[0]}
        </span>
      </div>

      {layout.clamped.length > 0 && (
        <p className="mt-2 text-xs text-amber-600 dark:text-amber-400">
          {clampNote(layout.clamped.map(sourceLabel).join(", "))}
        </p>
      )}
    </div>
  );
}

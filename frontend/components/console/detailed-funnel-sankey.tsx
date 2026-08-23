"use client";

import { useMemo } from "react";

export interface DetailedFunnelStage {
  label: string;
  value: number;
  detail?: string;
}

export interface DetailedFunnelStream {
  id: string;
  label: string;
  stages: DetailedFunnelStage[];
  startColumn?: number;
  color?: string;
}

export interface DetailedFunnelAnnotation {
  label: string;
  value: number;
  detail: string;
}

const COLORS = ["#2563eb", "#7c3aed", "#0f766e", "#d97706", "#db2777", "#0891b2", "#4f46e5"];
const DROP = "#cbd5e1";
const WIDTH = 1160;
const NODE_WIDTH = 13;
const TOP = 34;
const LANE_HEIGHT = 94;

function curve(x0: number, y0: number, x1: number, y1: number): string {
  const middle = (x0 + x1) / 2;
  return `M${x0},${y0} C${middle},${y0} ${middle},${y1} ${x1},${y1}`;
}

/**
 * Draws several independently provable cohort paths in one Sankey canvas.
 * Streams never receive a fabricated link across an identity or unit boundary:
 * the caller starts the next observed cohort in its own lane and labels the
 * boundary in the surrounding copy.
 */
export function DetailedFunnelSankey({
  streams,
  annotations = [],
  ariaLabel,
}: {
  streams: DetailedFunnelStream[];
  annotations?: DetailedFunnelAnnotation[];
  ariaLabel: string;
}) {
  const maxColumns = useMemo(
    () => Math.max(2, ...streams.map((stream) => (stream.startColumn ?? 0) + stream.stages.length)),
    [streams],
  );
  const height = TOP + Math.max(1, streams.length) * LANE_HEIGHT + 18;
  const xFor = (column: number) => (column * (WIDTH - NODE_WIDTH - 220)) / (maxColumns - 1) + 150;

  if (streams.length === 0) return null;

  return (
    <div className="text-white dark:text-gray-900">
      <svg viewBox={`0 0 ${WIDTH} ${height}`} className="w-full" role="img" aria-label={ariaLabel}>
        {streams.map((stream, streamIndex) => {
          const startColumn = stream.startColumn ?? 0;
          const color = stream.color ?? COLORS[streamIndex % COLORS.length];
          const laneTop = TOP + streamIndex * LANE_HEIGHT;
          const maxValue = Math.max(...stream.stages.map((stage) => stage.value), 1);
          const scale = (LANE_HEIGHT - 30) / maxValue;
          const nodes = stream.stages.map((stage, stageIndex) => ({
            ...stage,
            x: xFor(startColumn + stageIndex),
            height: Math.max(stage.value * scale, stage.value > 0 ? 5 : 0),
            y: laneTop,
          }));

          return (
            <g key={stream.id}>
              <text x={0} y={laneTop + 11} className="fill-gray-700 dark:fill-gray-300" style={{ fontSize: 12, fontWeight: 700 }}>
                {stream.label}
              </text>
              {nodes.slice(0, -1).map((node, index) => {
                const target = nodes[index + 1];
                const kept = Math.min(node.value, target.value);
                const dropped = Math.max(node.value - target.value, 0);
                const keptWidth = Math.max(kept * scale, kept > 0 ? 2 : 0);
                const dropWidth = Math.max(dropped * scale, dropped > 0 ? 2 : 0);
                return (
                  <g key={`${stream.id}:${index}`}>
                    {kept > 0 && <path d={curve(node.x + NODE_WIDTH, node.y + keptWidth / 2, target.x, target.y + keptWidth / 2)} fill="none" stroke={color} strokeWidth={keptWidth} opacity={0.58}><title>{`${stream.label}: ${node.label} → ${target.label}: ${kept}`}</title></path>}
                    {dropped > 0 && <path d={curve(node.x + NODE_WIDTH, node.y + keptWidth + dropWidth / 2, target.x, laneTop + LANE_HEIGHT - 10 - dropWidth / 2)} fill="none" stroke={DROP} strokeWidth={dropWidth} opacity={0.68}><title>{`${stream.label}: не дошли до ${target.label}: ${dropped}`}</title></path>}
                  </g>
                );
              })}
              {nodes.map((node, index) => {
                const last = index === nodes.length - 1;
                const labelX = last ? node.x + NODE_WIDTH + 8 : node.x - 7;
                const anchor = last ? "start" : "end";
                return (
                  <g key={`${stream.id}:node:${node.label}`}>
                    <rect x={node.x} y={node.y} width={NODE_WIDTH} height={node.height} rx={2} fill={color}><title>{`${node.label}: ${node.value}${node.detail ? ` (${node.detail})` : ""}`}</title></rect>
                    <text x={labelX} y={node.y + 10} textAnchor={anchor} className="fill-gray-700 dark:fill-gray-300" style={{ fontSize: 11, fontWeight: 600 }}>{node.label}</text>
                    <text x={labelX} y={node.y + 23} textAnchor={anchor} className="fill-blue-700 dark:fill-blue-300" style={{ fontSize: 12, fontWeight: 800 }}>{node.value}</text>
                    {node.detail && <text x={labelX} y={node.y + 35} textAnchor={anchor} className="fill-gray-400 dark:fill-gray-500" style={{ fontSize: 10 }}>{node.detail}</text>}
                  </g>
                );
              })}
            </g>
          );
        })}
      </svg>
      {annotations.length > 0 && <div className="mt-3 flex flex-wrap gap-x-5 gap-y-2 border-t border-dashed border-gray-200 pt-3 text-xs text-gray-500 dark:border-gray-700 dark:text-gray-400">{annotations.map((annotation) => <span key={annotation.label}><span className="font-semibold text-gray-800 dark:text-gray-100">{annotation.label}: {annotation.value}</span> · {annotation.detail}</span>)}</div>}
    </div>
  );
}

"use client";

import { useMemo } from "react";
import type { AdminFunnelJourneySlice, AdminFunnelJourneyStage } from "@/lib/types";

/**
 * One spine, one scale, one unit.
 *
 * The funnel this replaces drew a lane per data source and scaled EACH lane by
 * its own maximum, so a stage of 10 people and a stage of 522 visits rendered
 * as ribbons of identical thickness and nothing could be compared by eye. Here
 * every stage is measured against the same first stage, so the picture is the
 * conversion.
 *
 * Drop-off leaves downward in grey and carries its own number: the question a
 * funnel exists to answer is where people stop, and that used to be readable
 * only by subtracting two labels.
 *
 * A dark segment is a transition the data cannot observe. It is drawn hatched
 * and labelled rather than smoothed over, because a smooth ribbon across an
 * unobservable gap is exactly the quiet lie this chart is meant to avoid.
 */
export interface FunnelSpineProps {
  stages: AdminFunnelJourneyStage[];
  slices?: AdminFunnelJourneySlice[];
  darkSegment?: string;
  darkNote?: string;
  scale?: "linear" | "log";
  ariaLabel: string;
  sliceLabel?: (stageKey: string, partKey: string) => string;
}

const WIDTH = 1160;
const LEFT = 60;
const RIGHT = 96;
const BASE = 352;
const GUTTER = 416;
const MAX_BAR = 232;
const NODE_WIDTH = 9;
const ROW_Y = [76, 122];

const SPINE_COLOR = "#2563eb";
const DROP_COLOR = "#6b7280";

function curveArea(x0: number, top0: number, x1: number, top1: number, bottom: number): string {
  const mid = (x0 + x1) / 2;
  return `M${x0},${bottom} L${x0},${top0} C${mid},${top0} ${mid},${top1} ${x1},${top1} L${x1},${bottom} Z`;
}

export function FunnelSpine({
  stages,
  slices = [],
  darkSegment,
  darkNote,
  scale = "log",
  ariaLabel,
  sliceLabel,
}: FunnelSpineProps) {
  const nodes = useMemo(() => {
    if (stages.length === 0) return [];
    const max = Math.max(...stages.map((stage) => stage.value), 1);
    const step = (WIDTH - LEFT - RIGHT) / Math.max(stages.length - 1, 1);
    const fraction = (value: number) => {
      if (value <= 0) return 0;
      return scale === "linear" ? value / max : Math.log10(1 + value) / Math.log10(1 + max);
    };
    return stages.map((stage, index) => {
      const height = Math.max(fraction(stage.value) * MAX_BAR, stage.value > 0 ? 3 : 0);
      return {
        ...stage,
        x: LEFT + index * step,
        height,
        y: BASE - height,
        rowY: ROW_Y[index % 2],
        fraction,
        step,
      };
    });
  }, [scale, stages]);

  if (nodes.length === 0) return null;

  const step = nodes[0].step;
  const scaleOf = nodes[0].fraction;
  const barOf = (value: number) => Math.max(scaleOf(value) * MAX_BAR, value > 0 ? 3 : 0);

  return (
    <div>
      <svg viewBox={`0 0 ${WIDTH} 500`} className="w-full" role="img" aria-label={ariaLabel}>
        <defs>
          <pattern id="funnel-dark" width={7} height={7} patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
            <rect width={2.6} height={7} fill={DROP_COLOR} opacity={0.85} />
          </pattern>
        </defs>

        {nodes.slice(0, -1).map((node, index) => {
          const next = nodes[index + 1];
          const kept = Math.min(node.value, next.value);
          const dropped = Math.max(node.value - next.value, 0);
          const keptHeight = barOf(kept);
          const dropHeight = Math.max(node.height - keptHeight, 0);
          const mid = (node.x + next.x) / 2;
          const isDark = darkSegment === `${node.key}→${next.key}`;
          return (
            <g key={`${node.key}:${next.key}`}>
              {kept > 0 && (
                <path
                  d={curveArea(node.x + NODE_WIDTH, BASE - keptHeight, next.x, next.y, BASE)}
                  fill={isDark ? "url(#funnel-dark)" : SPINE_COLOR}
                  opacity={isDark ? 0.4 : 0.26}
                >
                  <title>{`${node.label} → ${next.label}: ${kept}`}</title>
                </path>
              )}
              {dropped > 0 && dropHeight > 0 && (
                <path
                  d={curveArea(node.x + NODE_WIDTH, node.y, next.x - 14, GUTTER - dropHeight, GUTTER)}
                  fill={DROP_COLOR}
                  opacity={0.3}
                >
                  <title>{`Не дошли до «${next.label}»: ${dropped}`}</title>
                </path>
              )}
              {dropped > 0 && dropHeight > 0 && (
                <text x={next.x - 16} y={GUTTER + 13} textAnchor="end" fontSize={10} className="fill-gray-500 dark:fill-gray-400">
                  {`\u2212${dropped}`}
                </text>
              )}
              {isDark && darkNote && (
                <text x={mid} y={26} textAnchor="middle" fontSize={9} className="fill-gray-500 dark:fill-gray-400">
                  {darkNote}
                </text>
              )}
            </g>
          );
        })}

        {nodes.map((node, index) => {
          const rate = index === 0 ? null : Math.round((node.value / Math.max(nodes[index - 1].value, 1)) * 100);
          const tailward = index === nodes.length - 1;
          const textX = tailward ? node.x - 6 : node.x + 8;
          const anchor = tailward ? "end" : "start";
          return (
            <g key={node.key}>
              <rect x={node.x} y={node.y} width={NODE_WIDTH} height={node.height} rx={2} fill={SPINE_COLOR}>
                <title>{`${node.label}: ${node.value} (${node.source})`}</title>
              </rect>
              <line
                x1={node.x + NODE_WIDTH / 2}
                y1={node.rowY + 8}
                x2={node.x + NODE_WIDTH / 2}
                y2={node.y - 4}
                stroke={SPINE_COLOR}
                strokeWidth={0.8}
                opacity={0.35}
              />
              <text x={textX} y={node.rowY - 16} textAnchor={anchor} fontSize={11} fontWeight={600} className="fill-gray-700 dark:fill-gray-200">
                {node.label}
              </text>
              <text x={textX} y={node.rowY} textAnchor={anchor} fontSize={16} fontWeight={800} fill={SPINE_COLOR}>
                {node.value}
              </text>
              {rate !== null && (
                <text x={textX} y={node.rowY + 13} textAnchor={anchor} fontSize={9} className="fill-gray-400 dark:fill-gray-500">
                  {`${rate}% от предыдущего`}
                </text>
              )}
            </g>
          );
        })}

        {slices.map((slice, sliceIndex) => {
          const anchor = nodes.find((node) => node.key === slice.stage_key);
          if (!anchor) return null;
          const total = slice.parts.reduce((sum, part) => sum + part.value, 0);
          if (total <= 0) return null;
          const barWidth = step * 1.6;
          const rowY = GUTTER + 40 + sliceIndex * 34;
          let cursor = Math.min(anchor.x, WIDTH - RIGHT - barWidth);
          return (
            <g key={slice.stage_key}>
              <text x={anchor.x} y={rowY - 6} fontSize={9} className="fill-gray-500 dark:fill-gray-400">
                {slice.label}
              </text>
              {slice.parts.map((part, partIndex) => {
                const width = (barWidth * part.value) / total;
                const x = cursor;
                cursor += width;
                const name = sliceLabel ? sliceLabel(slice.stage_key, part.key) : part.key;
                return (
                  <g key={`${slice.stage_key}:${part.key}`}>
                    <rect x={x} y={rowY} width={Math.max(width - 1.5, 1)} height={10} rx={2} fill={SPINE_COLOR} opacity={0.88 - partIndex * 0.14}>
                      <title>{`${name}: ${part.value}`}</title>
                    </rect>
                    {width > 48 && (
                      <text x={x + 2} y={rowY + 22} fontSize={9} className="fill-gray-500 dark:fill-gray-400">
                        {`${name} ${part.value}`}
                      </text>
                    )}
                  </g>
                );
              })}
            </g>
          );
        })}

        <line x1={LEFT - 16} y1={BASE} x2={WIDTH - RIGHT + 14} y2={BASE} className="stroke-gray-200 dark:stroke-gray-700" />
        <text x={LEFT - 16} y={GUTTER + 13} fontSize={10} className="fill-gray-500 dark:fill-gray-400">
          отвал
        </text>
      </svg>
    </div>
  );
}

/**
 * Geometry for the admin channel funnel Sankey.
 *
 * Kept out of the component so the layout can be pinned by unit tests: the
 * chart's whole job is to be countable, and the thing that makes it countable
 * is arithmetic (stage nesting, drop-off sizing, clamping), not SVG.
 *
 * Model: every stage after the first is a subset of the stage before it, per
 * traffic source. What leaves between two stages is drawn as an explicit
 * drop-off node, so the picture accounts for 100% of the people who entered —
 * a funnel that only draws survivors reads as a thread and says nothing about
 * where the loss happened.
 */

export interface ChannelFunnelSeries {
  source: string;
  /** One value per stage, stage 0 first. Must be countable in people, not events. */
  values: number[];
}

export interface FunnelNode {
  id: string;
  column: number;
  label: string;
  value: number;
  x: number;
  y: number;
  height: number;
  kind: "source" | "passed" | "drop";
  /** Share of the funnel entry, 0..1. Only set on passed nodes. */
  share?: number;
  source?: string;
}

export interface FunnelLink {
  id: string;
  source: string;
  target: string;
  value: number;
  x0: number;
  y0: number;
  x1: number;
  y1: number;
  width: number;
  kind: "passed" | "drop";
}

export interface FunnelLayout {
  width: number;
  height: number;
  nodeWidth: number;
  nodes: FunnelNode[];
  links: FunnelLink[];
  /** Sources whose stage values were not nested and had to be clamped. */
  clamped: string[];
  entryTotal: number;
}

export interface FunnelLayoutOptions {
  width?: number;
  height?: number;
  nodeWidth?: number;
  nodeGap?: number;
  minNodeHeight?: number;
  heroNodeHeight?: number;
  minLinkWidth?: number;
}

const DEFAULTS = {
  width: 920,
  height: 220,
  nodeWidth: 11,
  nodeGap: 12,
  minNodeHeight: 3,
  heroNodeHeight: 20,
  minLinkWidth: 1,
};

/**
 * Clamps each series so stage N never exceeds stage N-1, and reports which
 * sources needed it.
 *
 * Metrika samples each goal's unique-user count independently, so a later
 * stage can come back marginally larger than the stage it nests inside;
 * drawing that literally inverts the ribbon. Clamping keeps the picture
 * readable and the returned `clamped` list keeps it honest — the caller is
 * expected to surface it rather than swallow it.
 */
export function clampSeries(channels: ChannelFunnelSeries[]): { series: ChannelFunnelSeries[]; clamped: string[] } {
  const clamped: string[] = [];
  const series = channels.map((c) => {
    const values: number[] = [];
    let dirty = false;
    c.values.forEach((v, i) => {
      const capped = i === 0 ? v : Math.min(v, values[i - 1]);
      if (capped !== v) dirty = true;
      values.push(capped);
    });
    if (dirty) clamped.push(c.source);
    return { source: c.source, values };
  });
  return { series, clamped };
}

/**
 * Builds node and link geometry for a stage-by-stage funnel with drop-off.
 *
 * Column 0 carries one node per traffic source. Every later column carries
 * exactly two nodes: the survivors of that stage on top, and everyone lost
 * since the previous stage underneath. Ribbons keep their source's colour all
 * the way through, so a channel's loss stays attributable to that channel.
 *
 * @param channels one series per traffic source, values already counted in people
 * @param stageLabels label per stage; its length defines the column count
 * @param dropLabels label per transition, i.e. one fewer than stageLabels
 */
export function buildChannelFunnelLayout(
  channels: ChannelFunnelSeries[],
  stageLabels: string[],
  dropLabels: string[],
  options: FunnelLayoutOptions = {},
): FunnelLayout {
  const opt = { ...DEFAULTS, ...options };
  const stages = stageLabels.length;

  const { series, clamped } = clampSeries(
    channels.filter((c) => (c.values[0] ?? 0) > 0).map((c) => ({ source: c.source, values: c.values.slice(0, stages) })),
  );
  series.sort((a, b) => b.values[0] - a.values[0]);

  const entryTotal = series.reduce((sum, c) => sum + c.values[0], 0);
  const nodes: FunnelNode[] = [];
  const links: FunnelLink[] = [];
  if (entryTotal === 0 || stages < 2) {
    return { width: opt.width, height: opt.height, nodeWidth: opt.nodeWidth, nodes, links, clamped, entryTotal };
  }

  const colX = Array.from({ length: stages }, (_, i) => (i * (opt.width - opt.nodeWidth)) / (stages - 1));
  const maxNodesInColumn = Math.max(series.length, 2);
  const scale = (opt.height - (maxNodesInColumn - 1) * opt.nodeGap) / entryTotal;
  const stageTotals = Array.from({ length: stages }, (_, i) => series.reduce((sum, c) => sum + c.values[i], 0));

  let y = 0;
  series.forEach((c) => {
    const height = Math.max(c.values[0] * scale, opt.minNodeHeight);
    nodes.push({
      id: `src:${c.source}`,
      column: 0,
      label: c.source,
      value: c.values[0],
      x: colX[0],
      y,
      height,
      kind: "source",
      source: c.source,
    });
    y += height + opt.nodeGap;
  });

  for (let stage = 1; stage < stages; stage++) {
    const passedValue = stageTotals[stage];
    const dropValue = stageTotals[stage - 1] - passedValue;
    const isLast = stage === stages - 1;
    const passedHeight = Math.max(passedValue * scale, isLast ? opt.heroNodeHeight : opt.minNodeHeight);
    nodes.push({
      id: `passed:${stage}`,
      column: stage,
      label: stageLabels[stage],
      value: passedValue,
      x: colX[stage],
      y: 0,
      height: passedHeight,
      kind: "passed",
      share: passedValue / entryTotal,
    });
    nodes.push({
      id: `drop:${stage}`,
      column: stage,
      label: dropLabels[stage - 1],
      value: dropValue,
      x: colX[stage],
      y: passedHeight + opt.nodeGap,
      height: dropValue > 0 ? Math.max(dropValue * scale, opt.minNodeHeight) : 0,
      kind: "drop",
    });
  }

  const nodeById = new Map(nodes.map((n) => [n.id, n]));
  const outCursor = new Map<string, number>();
  const inCursor = new Map<string, number>();
  const cursor = (map: Map<string, number>, id: string) => map.get(id) ?? 0;

  for (let stage = 1; stage < stages; stage++) {
    const passed = nodeById.get(`passed:${stage}`)!;
    const drop = nodeById.get(`drop:${stage}`)!;
    series.forEach((c) => {
      const from = nodeById.get(stage === 1 ? `src:${c.source}` : `passed:${stage - 1}`)!;
      const kept = c.values[stage];
      const lost = c.values[stage - 1] - kept;

      ([
        [passed, kept, "passed" as const],
        [drop, lost, "drop" as const],
      ] as const).forEach(([target, value, kind]) => {
        if (value <= 0) return;
        const width = Math.max(value * scale, opt.minLinkWidth);
        const y0 = from.y + cursor(outCursor, from.id) + width / 2;
        const y1 = target.y + cursor(inCursor, target.id) + width / 2;
        outCursor.set(from.id, cursor(outCursor, from.id) + width);
        inCursor.set(target.id, cursor(inCursor, target.id) + width);
        links.push({
          id: `${c.source}:${stage}:${kind}`,
          source: c.source,
          target: target.id,
          value,
          x0: from.x + opt.nodeWidth,
          y0,
          x1: target.x,
          y1,
          width,
          kind,
        });
      });
    });
  }

  const height = Math.max(opt.height, ...nodes.map((n) => n.y + n.height));
  return { width: opt.width, height, nodeWidth: opt.nodeWidth, nodes, links, clamped, entryTotal };
}

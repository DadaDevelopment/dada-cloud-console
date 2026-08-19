/**
 * Unit tests for the admin channel funnel geometry (lib/channel-funnel.ts).
 *
 * Run with Node's built-in test runner and type stripping:
 *
 *   cd frontend && npm run test:unit
 *
 * These pin the properties that make the chart readable as a funnel:
 * conservation (survivors + drop-off = the stage above), non-inverting
 * ribbons, a visible terminal node when the win is a rounding error next to
 * the entry, and a reported clamp instead of a silent one.
 */

import test from "node:test";
import assert from "node:assert/strict";

import { buildChannelFunnelLayout, clampSeries, type ChannelFunnelSeries } from "./channel-funnel.ts";

const STAGES = ["Пришли", "Открыли регистрацию", "Выбрали способ", "Зарегистрировались"];
const DROPS = ["Ушли с сайта", "Не выбрали способ", "Не завершили"];

/** Production shape, 30 days, Metrika goal-users (2026-08-19). */
const LIVE: ChannelFunnelSeries[] = [
  { source: "Direct traffic", values: [298, 47, 34, 5] },
  { source: "Internal traffic", values: [29, 7, 4, 1] },
  { source: "Search engine traffic", values: [19, 4, 2, 2] },
  { source: "Link traffic", values: [5, 0, 0, 0] },
];

test("survivors plus drop-off equal the stage above", () => {
  const layout = buildChannelFunnelLayout(LIVE, STAGES, DROPS);
  const entry = LIVE.reduce((sum, c) => sum + c.values[0], 0);
  let previous = entry;
  for (let stage = 1; stage < STAGES.length; stage++) {
    const passed = layout.nodes.find((n) => n.id === `passed:${stage}`)!;
    const drop = layout.nodes.find((n) => n.id === `drop:${stage}`)!;
    assert.equal(passed.value + drop.value, previous, `stage ${stage} does not conserve people`);
    previous = passed.value;
  }
  assert.equal(layout.entryTotal, entry);
});

test("every link carries a positive value and no ribbon inverts", () => {
  const layout = buildChannelFunnelLayout(LIVE, STAGES, DROPS);
  assert.ok(layout.links.length > 0);
  for (const link of layout.links) {
    assert.ok(link.value > 0, `link ${link.id} has value ${link.value}`);
    assert.ok(link.width > 0, `link ${link.id} has width ${link.width}`);
    assert.ok(link.x1 > link.x0, `link ${link.id} runs backwards`);
  }
});

test("a source that never reaches stage 1 contributes only a drop-off ribbon", () => {
  const layout = buildChannelFunnelLayout(LIVE, STAGES, DROPS);
  const link = layout.links.filter((l) => l.source === "Link traffic");
  assert.deepEqual(
    link.map((l) => l.kind),
    ["drop"],
  );
  assert.equal(link[0].value, 5);
});

test("the terminal node stays visible when its share is a rounding error", () => {
  const layout = buildChannelFunnelLayout(LIVE, STAGES, DROPS, { heroNodeHeight: 20 });
  const last = layout.nodes.find((n) => n.id === `passed:${STAGES.length - 1}`)!;
  assert.equal(last.value, 8);
  assert.ok(last.height >= 20, `terminal node height ${last.height} would disappear`);
  assert.ok(last.share !== undefined && last.share < 0.03);
});

test("a non-nested stage is clamped and reported, not drawn as growth", () => {
  const { series, clamped } = clampSeries([{ source: "Direct traffic", values: [100, 10, 40] }]);
  assert.deepEqual(series[0].values, [100, 10, 10]);
  assert.deepEqual(clamped, ["Direct traffic"]);

  const layout = buildChannelFunnelLayout([{ source: "Direct traffic", values: [100, 10, 40] }], STAGES.slice(0, 3), DROPS.slice(0, 2));
  assert.deepEqual(layout.clamped, ["Direct traffic"]);
  assert.equal(layout.nodes.find((n) => n.id === "passed:2")!.value, 10);
});

test("empty input yields an empty layout instead of throwing", () => {
  const layout = buildChannelFunnelLayout([], STAGES, DROPS);
  assert.equal(layout.nodes.length, 0);
  assert.equal(layout.links.length, 0);
  assert.equal(layout.entryTotal, 0);
});

test("sources are ordered by entry volume", () => {
  const layout = buildChannelFunnelLayout(LIVE, STAGES, DROPS);
  const order = layout.nodes.filter((n) => n.kind === "source").map((n) => n.value);
  assert.deepEqual(order, [...order].sort((a, b) => b - a));
});

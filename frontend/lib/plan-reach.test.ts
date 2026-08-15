import assert from "node:assert/strict";
import test from "node:test";
import { parseVolumeSizeGB, pickTargetPlan, solutionReach } from "./plan-reach.ts";

const PLANS = [
  { key: "free", name: "Free", price_rub: 0, quotas: { storage_gb: 10, apps: 2 } },
  { key: "startup", name: "Startup", price_rub: 990, quotas: { storage_gb: 50, apps: 10 } },
  { key: "business", name: "Business", price_rub: 2990, quotas: { storage_gb: 200, apps: 50 } },
] as unknown as Parameters<typeof pickTargetPlan>[0];

test("reads catalog volume sizes as whole GB", () => {
  assert.equal(parseVolumeSizeGB("100Gi"), 100);
  assert.equal(parseVolumeSizeGB("512Mi"), 1);
  assert.equal(parseVolumeSizeGB("1Ti"), 1024);
  assert.equal(parseVolumeSizeGB("20"), 1);
});

test("an unparseable or absent size is unknown, not zero", () => {
  assert.equal(parseVolumeSizeGB(null), null);
  assert.equal(parseVolumeSizeGB(""), null);
  assert.equal(parseVolumeSizeGB("big"), null);
  assert.equal(parseVolumeSizeGB("10Pi"), null);
});

test("target plan clears the requested size, not merely the current limit", () => {
  const target = pickTargetPlan(PLANS, "storage_gb", { currentLimit: 10, required: 100 });
  assert.equal(
    target?.key,
    "business",
    "a 100 GB install offered Startup (50 GB) would be money spent to hit the same wall",
  );
});

test("with no requested size known it falls back to the next plan up", () => {
  const target = pickTargetPlan(PLANS, "storage_gb", { currentLimit: 10 });
  assert.equal(target?.key, "startup");
});

test("free plans are never a paid upgrade target", () => {
  const target = pickTargetPlan(PLANS, "apps", { currentLimit: null, required: 1 });
  assert.equal(target?.key, "startup");
});

test("nothing is offered when no plan clears the requirement", () => {
  assert.equal(pickTargetPlan(PLANS, "storage_gb", { required: 5000 }), null);
  assert.equal(pickTargetPlan(PLANS, "not_a_resource", { required: 1 }), null);
  assert.equal(pickTargetPlan(null, "storage_gb", { required: 1 }), null);
});

test("a tile bigger than the plan is locked and named its plan", () => {
  const reach = solutionReach("100Gi", 10, PLANS);
  assert.equal(reach.reachable, false);
  assert.equal(reach.requiredGB, 100);
  assert.equal(reach.plan?.key, "business");
});

test("a tile that fits is reachable", () => {
  const reach = solutionReach("5Gi", 10, PLANS);
  assert.equal(reach.reachable, true);
  assert.equal(reach.plan, null);
});

test("a tile exactly at the limit fits", () => {
  assert.equal(solutionReach("10Gi", 10, PLANS).reachable, true);
});

test("unknown data never locks a tile", () => {
  assert.equal(solutionReach(null, 10, PLANS).reachable, true, "a tile with no volume is not storage-bound");
  assert.equal(solutionReach("100Gi", null, PLANS).reachable, true, "an unlimited or unknown quota blocks nothing");
  assert.equal(solutionReach("weird", 10, PLANS).reachable, true, "a size we failed to parse is not evidence of a block");
});

test("a locked tile with no plan large enough still reports locked", () => {
  const reach = solutionReach("5Ti", 10, PLANS);
  assert.equal(reach.reachable, false);
  assert.equal(reach.plan, null, "no paid plan holds 5 TiB, so there is nothing to sell -- the card must degrade to pricing");
});

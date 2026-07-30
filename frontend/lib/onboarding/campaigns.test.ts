/**
 * Unit tests for the onboarding campaign registry (lib/onboarding/campaigns.ts).
 *
 *   cd frontend && npm run test:unit
 *
 * The measured activation leak (2026-07-30: 9 of 16 real signups never triggered
 * a single build) is what `first-deploy` exists to close, so the two properties
 * pinned here are ordering and targeting: `selectPendingCampaign` returns the
 * FIRST pending match, so `first-deploy` must sit ahead of `agent` — otherwise a
 * brand-new empty project spends its one tour on the chat FAB — and the campaign
 * must stay anchored to the deploy hero, which only renders while the project has
 * zero apps. That anchor is the whole targeting rule: no `route` guard, no app
 * count in the provider.
 */

import test from "node:test";
import assert from "node:assert/strict";

import { ONBOARDING_CAMPAIGNS } from "./campaigns.ts";
import { selectPendingCampaign } from "./select.ts";

const FIRST_DEPLOY_TARGET = '[data-onboarding="first-deploy"]';

test("first-deploy outranks agent so an empty project gets the deploy tour", () => {
  const keys = ONBOARDING_CAMPAIGNS.map((c) => c.key);
  assert.ok(keys.indexOf("first-deploy") < keys.indexOf("agent"));
});

test("first-deploy is anchored to the deploy hero", () => {
  const campaign = ONBOARDING_CAMPAIGNS.find((c) => c.key === "first-deploy");
  assert.ok(campaign);
  assert.equal(campaign.steps.length, 1);
  assert.equal(campaign.steps[0].target, FIRST_DEPLOY_TARGET);
});

test("a fresh user on an empty project gets first-deploy", () => {
  const picked = selectPendingCampaign(ONBOARDING_CAMPAIGNS, {}, {
    pathname: "/projects/p1",
    hasTarget: () => true,
  });
  assert.equal(picked?.key, "first-deploy");
});

test("a project that already has an app falls through to agent", () => {
  const picked = selectPendingCampaign(ONBOARDING_CAMPAIGNS, {}, {
    pathname: "/projects/p1",
    hasTarget: (sel) => sel !== FIRST_DEPLOY_TARGET,
  });
  assert.equal(picked?.key, "agent");
});

test("a skipped first-deploy is not offered again", () => {
  const picked = selectPendingCampaign(ONBOARDING_CAMPAIGNS, { "first-deploy": "skipped" }, {
    pathname: "/projects/p1",
    hasTarget: () => true,
  });
  assert.equal(picked?.key, "agent");
});

test("every campaign key is url-path safe", () => {
  for (const c of ONBOARDING_CAMPAIGNS) {
    assert.equal(c.key, encodeURIComponent(c.key));
  }
});

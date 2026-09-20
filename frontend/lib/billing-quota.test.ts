import test from "node:test";
import assert from "node:assert/strict";
import { shouldShowQuotaGraceWarning, quotaPressure, quotaUpgradeHref } from "./billing-quota.ts";

test("does not claim an in-limit Free account is over quota just because grace is active", () => {
  assert.equal(shouldShowQuotaGraceWarning("25 сентября 2026 г.", []), false);
});

test("shows the grace warning when the API reports a real over-limit resource", () => {
  assert.equal(
    shouldShowQuotaGraceWarning("25 сентября 2026 г.", [{ resource: "apps", used: 2, limit: 1 }]),
    true,
  );
});

test("does not show grace copy after the grace deadline is absent", () => {
  assert.equal(shouldShowQuotaGraceWarning(null, [{ resource: "apps", used: 2, limit: 1 }]), false);
});

const ORDER = ["apps", "databases", "domains", "team_members"] as const;

test("separates resources already over the ceiling from those sitting exactly on it", () => {
  const { over, at } = quotaPressure(
    {
      apps: { used: 3, limit: 1 },
      databases: { used: 1, limit: 1 },
      domains: { used: 0, limit: 1 },
    },
    ORDER,
  );
  assert.deepEqual(over, ["apps"]);
  assert.deepEqual(at, ["databases"]);
});

test("treats a zero limit as unlimited, never as pressure", () => {
  const { over, at } = quotaPressure({ apps: { used: 7, limit: 0 } }, ORDER);
  assert.deepEqual(over, []);
  assert.deepEqual(at, []);
});

test("reports no pressure when the API sent no quotas at all", () => {
  const { over, at } = quotaPressure(null, ORDER);
  assert.deepEqual(over, []);
  assert.deepEqual(at, []);
});

test("keeps the reading order the user runs into resources in", () => {
  const { at } = quotaPressure(
    {
      team_members: { used: 1, limit: 1 },
      apps: { used: 1, limit: 1 },
      databases: { used: 1, limit: 1 },
    },
    ORDER,
  );
  assert.deepEqual(at, ["apps", "databases", "team_members"]);
});

test("sends an upgrade CTA to the in-console billing plans, not to marketing pricing", () => {
  assert.equal(quotaUpgradeHref("p-42"), "/projects/p-42/billing#billing-plans");
});

test("returns no upgrade href when the project is unknown, so no CTA to /projects/undefined", () => {
  assert.equal(quotaUpgradeHref(null), null);
  assert.equal(quotaUpgradeHref(undefined), null);
  assert.equal(quotaUpgradeHref(""), null);
});

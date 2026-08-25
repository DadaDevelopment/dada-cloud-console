import test from "node:test";
import assert from "node:assert/strict";
import { shouldShowQuotaGraceWarning } from "./billing-quota.ts";

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

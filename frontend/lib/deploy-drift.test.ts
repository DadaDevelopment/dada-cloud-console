import assert from "node:assert/strict";
import test from "node:test";

import { evaluateDeployDrift } from "./deploy-drift.ts";
import type { Deployment } from "./types.ts";

function makeDeployment(overrides: Partial<Deployment>): Deployment {
  return {
    id: "dep-id",
    environment_id: "env-id",
    app_name: "app",
    image_uri: "harbor.example/proj/app@sha256:aaa",
    trigger: "push",
    is_current: false,
    created_at: "2026-08-15T00:00:00Z",
    updated_at: "2026-08-15T00:00:00Z",
    ...overrides,
  };
}

test("evaluateDeployDrift flags the megafactory shape: newest deploy never reached the pod", () => {
  const latest = makeDeployment({ id: "newest", is_current: false, created_at: "2026-08-14T23:20:00Z" });
  const running = makeDeployment({ id: "oldest", is_current: true, created_at: "2026-08-14T22:58:00Z" });
  assert.deepEqual(evaluateDeployDrift([latest, running]), {
    currentDeploymentId: "oldest",
    latestDeploymentId: "newest",
  });
});

test("evaluateDeployDrift is null when the newest deployment is already current", () => {
  const latest = makeDeployment({ id: "newest", is_current: true });
  const older = makeDeployment({ id: "older", is_current: false });
  assert.equal(evaluateDeployDrift([latest, older]), null);
});

test("evaluateDeployDrift is null with fewer than two deployments", () => {
  assert.equal(evaluateDeployDrift([]), null);
  assert.equal(evaluateDeployDrift([makeDeployment({ is_current: false })]), null);
});

test("evaluateDeployDrift is null when nothing is marked current (running image unknown)", () => {
  const latest = makeDeployment({ id: "newest", is_current: false });
  const older = makeDeployment({ id: "older", is_current: false });
  assert.equal(evaluateDeployDrift([latest, older]), null);
});

test("evaluateDeployDrift is null for null/undefined input", () => {
  assert.equal(evaluateDeployDrift(null), null);
  assert.equal(evaluateDeployDrift(undefined), null);
});

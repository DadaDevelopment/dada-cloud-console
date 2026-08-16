import assert from "node:assert/strict";
import test from "node:test";

import { selectCurrentDeployment, isNewestDeployment, isPendingDeployment, shortImageRef } from "./current-deployment.ts";
import type { Deployment } from "./types.ts";

function makeDeployment(overrides: Partial<Deployment>): Deployment {
  return {
    id: "dep-id",
    environment_id: "env-id",
    app_name: "app",
    image_uri: "harbor.example/proj/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    trigger: "manual",
    is_current: false,
    created_at: "2026-08-16T16:44:04Z",
    updated_at: "2026-08-16T16:44:04Z",
    ...overrides,
  };
}

test("selectCurrentDeployment picks the deployment marked is_current", () => {
  const newest = makeDeployment({ id: "newest", is_current: false, created_at: "2026-08-16T16:45:29Z" });
  const running = makeDeployment({ id: "running", is_current: true, created_at: "2026-08-16T16:20:00Z" });
  const result = selectCurrentDeployment([newest, running], new Date("2026-08-16T16:46:00Z").getTime());
  assert.deepEqual(result, { kind: "current", deployment: running });
});

test("selectCurrentDeployment reads the megafactory gap as pending, not silence", () => {
  const newest = makeDeployment({ id: "newest", is_current: false, created_at: "2026-08-16T16:44:04Z" });
  const now = new Date("2026-08-16T16:45:29Z").getTime();
  const result = selectCurrentDeployment([newest], now);
  assert.deepEqual(result, { kind: "pending", deployment: newest });
});

test("selectCurrentDeployment is null once the pending window has passed with nothing landed", () => {
  const newest = makeDeployment({ id: "newest", is_current: false, created_at: "2026-08-16T16:00:00Z" });
  const now = new Date("2026-08-16T16:30:00Z").getTime();
  assert.equal(selectCurrentDeployment([newest], now), null);
});

test("selectCurrentDeployment is null for empty/null/undefined input", () => {
  assert.equal(selectCurrentDeployment([]), null);
  assert.equal(selectCurrentDeployment(null), null);
  assert.equal(selectCurrentDeployment(undefined), null);
});

test("isNewestDeployment is true only for deployments[0]", () => {
  const newest = makeDeployment({ id: "newest" });
  const older = makeDeployment({ id: "older" });
  assert.equal(isNewestDeployment("newest", [newest, older]), true);
  assert.equal(isNewestDeployment("older", [newest, older]), false);
  assert.equal(isNewestDeployment("newest", []), false);
  assert.equal(isNewestDeployment("newest", null), false);
});

test("isPendingDeployment marks the newest row only while it is plausibly still rolling out", () => {
  const newest = makeDeployment({ id: "newest", created_at: "2026-08-16T16:44:04Z" });
  const older = makeDeployment({ id: "older", created_at: "2026-08-16T16:20:00Z" });
  const inWindow = new Date("2026-08-16T16:45:29Z").getTime();
  assert.equal(isPendingDeployment("newest", [newest, older], inWindow), true);
  assert.equal(isPendingDeployment("older", [newest, older], inWindow), false);
});

test("isPendingDeployment stops claiming a rollout that never landed", () => {
  const stranded = makeDeployment({ id: "stranded", created_at: "2026-08-16T16:00:00Z" });
  const wayLater = new Date("2026-08-16T18:00:00Z").getTime();
  assert.equal(isPendingDeployment("stranded", [stranded], wayLater), false);
});

test("isPendingDeployment yields to a confirmed current deployment", () => {
  const newest = makeDeployment({ id: "newest", created_at: "2026-08-16T16:44:04Z" });
  const running = makeDeployment({ id: "running", is_current: true, created_at: "2026-08-16T16:20:00Z" });
  const now = new Date("2026-08-16T16:45:00Z").getTime();
  assert.equal(isPendingDeployment("newest", [newest, running], now), false);
});

test("shortImageRef shortens a digest-pinned image but leaves a tag-pinned one alone", () => {
  assert.equal(
    shortImageRef("harbor.example/proj/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
    "harbor.example/proj/app@sha256:aaaaaaaaaaaa"
  );
  assert.equal(shortImageRef("harbor.example/proj/app:v2.0.0"), "harbor.example/proj/app:v2.0.0");
});

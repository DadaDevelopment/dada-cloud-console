import assert from "node:assert/strict";
import test from "node:test";

import { getAppNextSteps } from "./app-next-step.ts";

test("a web app without a custom domain is offered the domain step", () => {
  assert.deepEqual(getAppNextSteps({ hasCustomDomain: false, hasGitRepo: true }), [
    "connect_domain",
    "deploy_commit",
  ]);
});

test("a web app with its own domain is not asked to connect one again", () => {
  assert.deepEqual(getAppNextSteps({ hasCustomDomain: true, hasGitRepo: true }), ["deploy_commit"]);
});

test("an app with no repo is offered git instead of a commit deploy", () => {
  assert.deepEqual(getAppNextSteps({ hasCustomDomain: true, hasGitRepo: false }), ["connect_git"]);
});

test("a worker is told how to get an address instead of being sent to a custom domain", () => {
  const steps = getAppNextSteps({ hasCustomDomain: false, hasGitRepo: false, isWorker: true });
  assert.deepEqual(steps, ["publish_web", "connect_git"]);
  assert.ok(!steps.includes("connect_domain"));
});

test("a worker is still told how to get an address when it already has a custom domain", () => {
  const steps = getAppNextSteps({ hasCustomDomain: true, hasGitRepo: true, isWorker: true });
  assert.deepEqual(steps, ["publish_web", "deploy_commit"]);
});

test("isWorker defaults to false so existing web apps keep the domain step", () => {
  assert.deepEqual(getAppNextSteps({ hasCustomDomain: false, hasGitRepo: false }), [
    "connect_domain",
    "connect_git",
  ]);
});

test("never returns more than three steps", () => {
  const steps = getAppNextSteps({ hasCustomDomain: false, hasGitRepo: true, isWorker: true });
  assert.ok(steps.length <= 3);
});

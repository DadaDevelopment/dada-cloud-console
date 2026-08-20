/**
 * Unit tests for lib/start-command-redeploy.ts -- the fix for the
 * crash-banner repair flow: PATCHing a start command only takes effect on
 * the app's NEXT deploy, so a first-day user following the banner's own CTA
 * would do exactly what they were told and stay crashlooping. These tests
 * pin the fix and its optimistic-but-ACID guardrail:
 *
 * 1. With autoRedeploy off (or the server not returning an operation), the
 *    function must resolve immediately without polling -- the plain
 *    settings-page editor keeps its pre-existing behaviour.
 * 2. With autoRedeploy on and an operation queued, it must poll to a
 *    terminal status and only report "applied" once the operation actually
 *    succeeded.
 * 3. A redeploy operation that ends Failed/Cancelled must never be reported
 *    as anything but "apply-failed" -- this is the regression this file
 *    guards: a version that returned "saved" the instant the PATCH itself
 *    succeeded (never awaiting the operation) is exactly the bug reported --
 *    the button would read as fixed while the app kept crashlooping.
 */
import { test } from "node:test";
import assert from "node:assert/strict";

import { saveStartCommand, type StartCommandSaveDeps } from "./start-command-redeploy.ts";
import type { Operation } from "./types.ts";

function makeOperation(overrides: Partial<Operation>): Operation {
  return {
    id: "op-1",
    actor_id: "user-1",
    project_id: "proj-1",
    action: "DeployImageVersion",
    resource_kind: "App",
    resource_name: "myapp",
    status: "Created",
    created_at: "2026-08-20T00:00:00Z",
    updated_at: "2026-08-20T00:00:00Z",
    ...overrides,
  };
}

test("saveStartCommand without autoRedeploy resolves immediately, no polling", async () => {
  let getOperationCalls = 0;
  const deps: StartCommandSaveDeps = {
    updateStartCommand: async () => ({ start_command: "python agent.py", message: "ok" }),
    getOperation: async () => {
      getOperationCalls += 1;
      return { operation: makeOperation({ status: "Ready" }) };
    },
  };

  const result = await saveStartCommand(deps, "python agent.py", false);

  assert.deepEqual(result, { status: "saved" });
  assert.equal(getOperationCalls, 0, "must not poll when autoRedeploy is off");
});

test("saveStartCommand with autoRedeploy polls to Ready and reports applied", async () => {
  const statuses: Operation["status"][] = ["Created", "Rendering", "CommittingToGit", "Ready"];
  let pollIndex = 0;
  const deps: StartCommandSaveDeps = {
    updateStartCommand: async () => ({
      start_command: "python agent.py",
      message: "ok",
      operation: makeOperation({ status: statuses[0] }),
    }),
    getOperation: async () => {
      pollIndex += 1;
      return { operation: makeOperation({ status: statuses[pollIndex] }) };
    },
    sleep: async () => undefined,
  };

  const result = await saveStartCommand(deps, "python agent.py", true);

  assert.deepEqual(result, { status: "applied" });
  assert.equal(pollIndex, statuses.length - 1, "must poll until the terminal status, not stop early");
});

test("saveStartCommand with autoRedeploy reports apply-failed on a Failed operation, never a false success", async () => {
  const deps: StartCommandSaveDeps = {
    updateStartCommand: async () => ({
      start_command: "python agent.py",
      message: "ok",
      operation: makeOperation({ status: "Rendering" }),
    }),
    getOperation: async () => ({
      operation: makeOperation({ status: "Failed", error_message: "chart render failed" }),
    }),
    sleep: async () => undefined,
  };

  const result = await saveStartCommand(deps, "python agent.py", true);

  assert.deepEqual(result, { status: "apply-failed", message: "chart render failed" });
});

test("saveStartCommand with autoRedeploy times out instead of reporting success if the operation never reaches a terminal status", async () => {
  const deps: StartCommandSaveDeps = {
    updateStartCommand: async () => ({
      start_command: "python agent.py",
      message: "ok",
      operation: makeOperation({ status: "Created" }),
    }),
    getOperation: async () => ({ operation: makeOperation({ status: "Rendering" }) }),
    sleep: async () => undefined,
    maxPolls: 3,
  };

  const result = await saveStartCommand(deps, "python agent.py", true);

  assert.deepEqual(result, { status: "apply-timeout" });
});

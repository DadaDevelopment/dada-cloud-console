/**
 * Unit tests for lib/port-redeploy.ts -- the fix for "diagnosis without a
 * lever": framework autodetection picks the app's port once at creation
 * time and the value used to be permanently fixed after that, so a wrong
 * detection (e.g. Vite's default 4173 for a process actually listening on
 * 3000) left the app on a permanent 502 with nothing to click inside the
 * product. These tests pin the fix and its optimistic-but-ACID guardrail,
 * mirrored from lib/start-command-redeploy.test.ts:
 *
 * 1. When the server does not queue a redeploy (no `operation` on the PATCH
 *    response), the function must resolve immediately without polling.
 * 2. When the server queues a redeploy, it must poll to a terminal status
 *    and only report "applied" once that operation actually succeeded.
 * 3. A redeploy operation that ends Failed/Cancelled must never be reported
 *    as anything but "apply-failed" -- reporting a bare "saved" the instant
 *    the PATCH itself succeeds, without awaiting the queued operation, would
 *    read as fixed in the UI while the app kept serving 502 on the old port.
 * 4. An operation that never reaches a terminal status within maxPolls must
 *    time out rather than silently report success.
 */
import { test } from "node:test";
import assert from "node:assert/strict";

import { savePort, type PortSaveDeps } from "./port-redeploy.ts";
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
    created_at: "2026-08-21T00:00:00Z",
    updated_at: "2026-08-21T00:00:00Z",
    ...overrides,
  };
}

test("savePort without a queued operation resolves immediately, no polling", async () => {
  let getOperationCalls = 0;
  const deps: PortSaveDeps = {
    updatePort: async () => ({ port: 3000, message: "ok" }),
    getOperation: async () => {
      getOperationCalls += 1;
      return { operation: makeOperation({ status: "Ready" }) };
    },
  };

  const result = await savePort(deps, 3000);

  assert.deepEqual(result, { status: "saved" });
  assert.equal(getOperationCalls, 0, "must not poll when the server did not queue a redeploy");
});

test("savePort with a queued operation polls to Ready and reports applied", async () => {
  const statuses: Operation["status"][] = ["Created", "Rendering", "CommittingToGit", "Ready"];
  let pollIndex = 0;
  const deps: PortSaveDeps = {
    updatePort: async () => ({
      port: 3000,
      message: "ok",
      operation: makeOperation({ status: statuses[0] }),
    }),
    getOperation: async () => {
      pollIndex += 1;
      return { operation: makeOperation({ status: statuses[pollIndex] }) };
    },
    sleep: async () => undefined,
  };

  const result = await savePort(deps, 3000);

  assert.deepEqual(result, { status: "applied" });
  assert.equal(pollIndex, statuses.length - 1, "must poll until the terminal status, not stop early");
});

test("savePort reports apply-failed on a Failed operation, never a false success", async () => {
  const deps: PortSaveDeps = {
    updatePort: async () => ({
      port: 3000,
      message: "ok",
      operation: makeOperation({ status: "Rendering" }),
    }),
    getOperation: async () => ({
      operation: makeOperation({ status: "Failed", error_message: "image failed to start on new port" }),
    }),
    sleep: async () => undefined,
  };

  const result = await savePort(deps, 3000);

  assert.deepEqual(result, { status: "apply-failed", message: "image failed to start on new port" });
});

test("savePort times out instead of reporting success if the operation never reaches a terminal status", async () => {
  const deps: PortSaveDeps = {
    updatePort: async () => ({
      port: 3000,
      message: "ok",
      operation: makeOperation({ status: "Created" }),
    }),
    getOperation: async () => ({ operation: makeOperation({ status: "Rendering" }) }),
    sleep: async () => undefined,
    maxPolls: 3,
  };

  const result = await savePort(deps, 3000);

  assert.deepEqual(result, { status: "apply-timeout" });
});

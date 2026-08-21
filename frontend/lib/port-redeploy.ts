import type { Operation } from "./types.ts";

/**
 * Terminal operation statuses, mirrored from lib/start-command-redeploy.ts
 * (same polling shape, same source of truth for what "done" means).
 */
const TERMINAL_OP_STATUSES = new Set(["Committed", "Ready", "Failed", "Cancelled"]);

export interface PortSaveDeps {
  updatePort: (port: number) => Promise<{ port: number; message: string; operation?: Operation }>;
  getOperation: (operationId: string) => Promise<{ operation: Operation }>;
  sleep?: (ms: number) => Promise<void>;
  maxPolls?: number;
}

export type SavePortResult =
  | { status: "saved" }
  | { status: "applied" }
  | { status: "apply-failed"; message: string }
  | { status: "apply-timeout" };

/**
 * Saves the port override and, when the server queues a redeploy to apply
 * it, waits for that operation to reach a terminal status before reporting
 * anything back to the caller.
 *
 * Same optimistic-but-ACID rule as saveStartCommand (lib/start-command-redeploy.ts):
 * a wrong-port app is stuck on a permanent 502 with no lever inside the
 * product until this PATCH actually lands, so the caller must never report
 * "saved" while the redeploy the server queued for it is still pending or
 * has failed -- the port change is otherwise no fix at all.
 */
export async function savePort(deps: PortSaveDeps, port: number): Promise<SavePortResult> {
  const res = await deps.updatePort(port);
  if (!res.operation) {
    return { status: "saved" };
  }

  const sleep = deps.sleep ?? ((ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms)));
  const maxPolls = deps.maxPolls ?? 60;

  let op = res.operation;
  for (let i = 0; i < maxPolls && !TERMINAL_OP_STATUSES.has(op.status); i++) {
    await sleep(1500);
    op = (await deps.getOperation(op.id)).operation;
  }

  if (op.status === "Failed" || op.status === "Cancelled") {
    return { status: "apply-failed", message: op.error_message || "" };
  }
  if (!TERMINAL_OP_STATUSES.has(op.status)) {
    return { status: "apply-timeout" };
  }
  return { status: "applied" };
}

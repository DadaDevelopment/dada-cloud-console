import type { Operation } from "./types.ts";

/**
 * Terminal operation statuses, mirrored from the polling loop already used
 * by frontend/components/app-servers/import-wizard.tsx for the same
 * queue-then-poll shape.
 */
const TERMINAL_OP_STATUSES = new Set(["Committed", "Ready", "Failed", "Cancelled"]);

export interface StartCommandSaveDeps {
  updateStartCommand: (
    value: string,
    redeploy: boolean
  ) => Promise<{ start_command: string; message: string; operation?: Operation }>;
  getOperation: (operationId: string) => Promise<{ operation: Operation }>;
  sleep?: (ms: number) => Promise<void>;
  maxPolls?: number;
}

export type SaveStartCommandResult =
  | { status: "saved" }
  | { status: "applied" }
  | { status: "apply-failed"; message: string }
  | { status: "apply-timeout" };

/**
 * Saves a start command and, when autoRedeploy is set, waits for the
 * redeploy operation the server queued to reach a terminal status before
 * reporting anything back to the caller.
 *
 * This is the fix for the crash-banner repair flow: PATCHing the start
 * command by itself only takes effect on the app's NEXT deploy, so a
 * crashlooping first-day user who follows the banner's own CTA would have
 * done exactly what they were told and stayed broken. When autoRedeploy is
 * true and the server queues a redeploy operation, this function polls it
 * to a terminal status and only ever returns "applied" if that operation
 * actually succeeded -- never a bare "saved" while the app might still be
 * running the old command. Optimistic-but-ACID UI is a house rule: the
 * caller must not synthesize success from the PATCH response alone.
 */
export async function saveStartCommand(
  deps: StartCommandSaveDeps,
  value: string,
  autoRedeploy: boolean
): Promise<SaveStartCommandResult> {
  const res = await deps.updateStartCommand(value, autoRedeploy);
  if (!autoRedeploy || !res.operation) {
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

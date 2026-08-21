import type { RecoveryPrompt } from "./types";

/**
 * Dismissal storage and navigation targets for the "we broke it, we fixed
 * it, here is the button" recovery prompt (GET /api/v1/recovery-prompt,
 * see backend/internal/api/platform_recovery.go).
 *
 * The backend already narrows eligibility to the exact people this is for
 * (failure predates the fix, no self-recovery, zero apps for the install
 * kind) -- this module only decides whether THIS BROWSER has already
 * dismissed the offer, and where the retry button should go. Dismissal is
 * client-side only, the same flat-localStorage-flag choice already made for
 * the live-URL banner (see lib/app-live-banner.ts): there is no server-side
 * "user dismissed the recovery offer" column, and a flag that never expires
 * is fine here because the key is scoped to the one failure instance
 * (`kind` + `failed_at`), so a NEW failure of the same kind gets its own key
 * and is never silently swallowed by an old dismissal.
 */

const DISMISS_KEY_PREFIX = "dada_recovery_prompt_dismissed:";

function dismissKey(kind: string, failedAt: string): string {
  return `${DISMISS_KEY_PREFIX}${kind}:${failedAt}`;
}

/** True whenever the flag cannot be read at all (no window, storage blocked), so the prompt fails closed rather than nagging. */
export function isRecoveryPromptDismissed(kind: string, failedAt: string): boolean {
  if (typeof window === "undefined") return true;
  try {
    return window.localStorage.getItem(dismissKey(kind, failedAt)) === "1";
  } catch {
    return true;
  }
}

/** Best-effort write. A blocked or full storage just means the prompt keeps reappearing, which is safe. */
export function dismissRecoveryPrompt(kind: string, failedAt: string): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(dismissKey(kind, failedAt), "1");
  } catch {
    return;
  }
}

/** Whether the prompt should render at all, given the backend's answer and this browser's own dismissal flag. */
export function shouldShowRecoveryPrompt(prompt: RecoveryPrompt | null): boolean {
  if (!prompt) return false;
  return !isRecoveryPromptDismissed(prompt.kind, prompt.failed_at);
}

/**
 * Where the retry button sends the user.
 *
 * `solution_install_env_failed` goes back to the apps page for the exact
 * project + environment the failed install was in -- the same page that
 * renders `TemplateDeployCards`, so the offer to retry lands right next to
 * the machinery that will actually retry it. `resource_name` (the app name
 * the failed install would have created) is not put in the URL: it names a
 * resource that failed to come into existence, so there is nothing at that
 * name to route to, and the query string is the wrong place for
 * user-identifying text anyway (see lib/ux-telemetry.ts's own rule against
 * that).
 *
 * `payment_recurring_forbidden` goes to the project's billing page, where
 * the checkout flow already lives.
 */
export function recoveryPromptHref(prompt: RecoveryPrompt): string {
  if (prompt.kind === "payment_recurring_forbidden") {
    return `/projects/${prompt.project_id}/billing`;
  }
  return `/projects/${prompt.project_id}/apps?envId=${prompt.environment_id}`;
}

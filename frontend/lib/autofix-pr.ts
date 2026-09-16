import type { CloudTask } from "@/lib/types";

export interface AutofixPr {
  id: string;
  url: string;
  created_at: string;
}

/**
 * Maximum number of fix pull requests any surface offers at once. A user who
 * ran auto-fix repeatedly on the same app accumulates one PR per run, and a
 * wall of links reads as noise rather than as the one action that ends the
 * failure loop.
 */
export const AUTOFIX_PR_LIMIT = 3;

/**
 * Selects the auto-fix pull requests worth showing next to a failed build.
 *
 * The agent reports its result by writing `pr_url` onto its cloud task, and
 * until now the only surface that read it was the runtime-crash banner on the
 * app page. That surface requires the app to have started at least once, so a
 * user whose build never produced a running container never saw the PR at all:
 * freitorsk (2026-09-12, app `chirping-kolyaska`) got PR #1 from the agent at
 * 10:49, then ran the same commit three more times and deleted the app at
 * 15:02 without the console ever naming the fix that was waiting on GitHub.
 *
 * Only `completed` tasks carrying a non-empty `pr_url` qualify: a running task
 * has nothing to link yet and a failed one produced no branch. Results are
 * deduplicated by URL because a retried run can report the same pull request
 * twice (cloud_tasks holds two rows for `fonbet_value/pull/54`), ordered
 * newest-first so the fix for the latest failure leads, and capped.
 */
export function selectAutofixPrs(tasks: CloudTask[] | undefined | null): AutofixPr[] {
  if (!tasks || tasks.length === 0) return [];
  const seen = new Set<string>();
  const picked: AutofixPr[] = [];
  const eligible = tasks.filter(
    (task) => task.status === "completed" && typeof task.pr_url === "string" && task.pr_url.trim() !== "",
  );
  const ordered = [...eligible].sort(
    (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
  );
  for (const task of ordered) {
    const url = task.pr_url!.trim();
    if (seen.has(url)) continue;
    seen.add(url);
    picked.push({ id: task.id, url, created_at: task.created_at });
    if (picked.length >= AUTOFIX_PR_LIMIT) break;
  }
  return picked;
}

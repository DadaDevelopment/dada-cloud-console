/**
 * Strips the machine-readable code the build agent prefixes onto a failed
 * build's `error_message`.
 *
 * The agent persists `"<fail_reason>: <detail>"` so the code survives even
 * where only the message column is read. The console already renders that code
 * as a translated sentence above the detail, so leaving the prefix in place
 * shows the reader the same fact twice, once in a language they did not ask
 * for.
 *
 * @param failReason - the build's `fail_reason`, if the API returned one
 * @param errorMessage - the build's raw `error_message`
 * @returns the detail alone, or the message unchanged when it carries no prefix
 */
export function buildFailureDetail(failReason?: string | null, errorMessage?: string | null): string {
  const message = (errorMessage ?? "").trim();
  if (!message || !failReason) return message;
  const prefix = `${failReason}: `;
  return message.startsWith(prefix) ? message.slice(prefix.length).trim() : message;
}

/**
 * Builds the failure context handed to the AI auto-fix run.
 *
 * The console used to send the branch, the commit ref and the commit message
 * and nothing else, so the agent was asked why a build broke while being told
 * only what was being built. The cause is the part it cannot derive.
 *
 * @param build - branch, commit ref and the persisted failure of the build
 * @returns a multi-line summary whose last lines name the reason and the cause
 */
export function buildFailureSummary(build: {
  branch: string;
  commitRef: string;
  commitMessage?: string | null;
  failReason?: string | null;
  errorMessage?: string | null;
}): string {
  let summary = `Build failed on branch ${build.branch} (${build.commitRef})`;
  if (build.commitMessage) summary += `: ${build.commitMessage}`;
  if (build.failReason) summary += `\nFailure reason: ${build.failReason}`;
  const detail = buildFailureDetail(build.failReason, build.errorMessage);
  if (detail) summary += `\nCause: ${detail}`;
  return summary;
}

/**
 * Reports whether a failed build can plausibly be fixed by editing the
 * repository, which is the only thing the auto-fix agent can do.
 *
 * Measured on thirty days of production failures: a third of them are git
 * authentication and orphaned GitHub App installations. No commit repairs
 * those -- the platform cannot even read the repo -- so offering an AI fix
 * there spends the user's attention on a run that is guaranteed to fail.
 *
 * @param failReason - the build's `fail_reason`
 */
export function isRepoFixable(failReason?: string | null): boolean {
  return failReason !== "git_auth_failed" && failReason !== "platform_error" && failReason !== "app_deleted";
}

/**
 * Reports whether the user can do anything about a failed build by
 * reconnecting the repository.
 *
 * Not the complement of {@link isRepoFixable}: that predicate answers "can the
 * auto-fix agent help", and it says no to three different reasons for three
 * different reasons. Only `git_auth_failed` is actually about the git link.
 * `platform_error` is our own failure (kkartov, 2026-08-19: five builds died on
 * `load repo 00000000-...: no rows in result set`, a race in our delete path),
 * and `app_deleted` means the build was cancelled because the app went away.
 * Sending either of those to "reconnect the repository" bills our fault to the
 * user's git setup and costs them a round trip that cannot help.
 *
 * @param failReason - the build's `fail_reason`
 */
export function needsRepoReconnect(failReason?: string | null): boolean {
  return failReason === "git_auth_failed";
}

/**
 * Reports whether the auto-fix lever may be offered for a build.
 *
 * Exists so every surface that shows a failed build gates the lever the same
 * way. The lever kept being written into one surface at a time and kept
 * landing where the user was not: it was first built into the deployments
 * feed, which measured zero visits, then moved onto the app page's build
 * card. Meanwhile the page a user actually reaches by following "view logs"
 * -- the build detail page -- offered only Rebuild. Sixty days of production
 * audit rows show the cost: `ViewBuildLogs` 91 and `TriggerBuild` 133 against
 * `TriggerAutofix` 7 from four actors, while 61% of failed builds were not
 * the user's code. A shipped lever nobody can reach is indistinguishable
 * from a lever that was never built.
 *
 * @param input - the build's status and failure, the app's git link, and
 *   whether this member may mutate the app at all
 */
export function canOfferAutofix(input: {
  status?: string | null;
  failReason?: string | null;
  hasGitRepo: boolean;
  canDeploy: boolean;
}): boolean {
  if (!input.canDeploy) return false;
  if (!input.hasGitRepo) return false;
  if (input.status !== "failed") return false;
  return isRepoFixable(input.failReason);
}

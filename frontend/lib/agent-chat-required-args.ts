/**
 * Backend-grounded required-argument specs for agent-chat write tools.
 *
 * Each entry lists the exact JSON body fields the corresponding REST handler
 * rejects a request for when blank -- the same field names the model's tool
 * args are deserialized into (see backend/internal/api/agent_chat.go's
 * agentChatArg, which reads these very keys off the raw args map). A tool
 * absent from this table has no known requirement here: unknownToolIsAllowed
 * below is what keeps that fail-open rather than fail-closed, so a tool this
 * table has not caught up with is never blocked by omission.
 *
 * file:line grounding for every entry:
 * - createS3Bucket: name OR bucket_name -> backend/internal/api/s3buckets.go
 *                   ("missing_name" fires only when BOTH are blank; either one alone
 *                   derives the other, so requiring both here would block the very
 *                   call the handler now accepts)
 * - createEndpoint: fqdn -> backend/internal/api/endpoints.go:184 ("missing_fqdn")
 * - createDatabase: name -> backend/internal/api/databases.go:355-356 ("name_required")
 *                   database -> backend/internal/api/databases.go:358-359 ("database_required")
 * - createApp: name -> backend/internal/api/apps.go:812-814 ("name_required", unconditional
 *              for every runtime; image is required too but only when the target app
 *              server is not a VM/compose one, so it is deliberately left out here rather
 *              than guessed)
 * - createProject: slug -> backend/internal/api/projects.go:115 (`binding:"required"`)
 * - connectGitRepo: repo_full_name -> backend/internal/api/gitrepos.go:1411 ("missing_repo_full_name")
 *                   app_name -> backend/internal/api/gitrepos.go:1414 ("missing_app_name")
 * - restoreDatabase: backup_id (or backupId) -> backend/internal/api/db_backups.go:327 ("missing_backup_id")
 */
interface RequiredArg {
  /** Any one of these keys being non-blank satisfies the requirement. */
  keys: string[];
  /** Label shown to the user; the first (canonical) key name. */
  label: string;
}

const REQUIRED_ARGS: Record<string, RequiredArg[]> = {
  createS3Bucket: [{ keys: ["name", "bucket_name"], label: "name" }],
  createEndpoint: [{ keys: ["fqdn"], label: "fqdn" }],
  createDatabase: [
    { keys: ["name"], label: "name" },
    { keys: ["database"], label: "database" },
  ],
  createApp: [{ keys: ["name"], label: "name" }],
  createProject: [{ keys: ["slug"], label: "slug" }],
  connectGitRepo: [
    { keys: ["repo_full_name"], label: "repo_full_name" },
    { keys: ["app_name"], label: "app_name" },
  ],
  restoreDatabase: [{ keys: ["backup_id", "backupId"], label: "backup_id" }],
};

function isBlank(value: unknown): boolean {
  return value === undefined || value === null || (typeof value === "string" && value.trim() === "");
}

/**
 * Returns the labels of every required argument missing (or blank) for a
 * pending write-tool call, in declaration order.
 *
 * A tool with no entry in REQUIRED_ARGS is not "unknown = blocked" but
 * "unknown = no known requirement": it always returns an empty list, so a
 * tool this table has not been taught about is never denied approval on that
 * account. Only the tools explicitly listed above -- each grounded in a real
 * backend rejection -- can ever produce a non-empty result.
 */
export function missingRequiredArgs(
  toolName: string,
  args: Record<string, unknown> | null | undefined,
): string[] {
  const specs = REQUIRED_ARGS[toolName];
  if (!specs || specs.length === 0) return [];
  const safeArgs = args ?? {};
  const missing: string[] = [];
  for (const spec of specs) {
    const present = spec.keys.some((key) => !isBlank(safeArgs[key]));
    if (!present) missing.push(spec.label);
  }
  return missing;
}

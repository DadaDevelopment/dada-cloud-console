/**
 * Resolves the `twin_of` field the backend injects into an app's
 * `summary_json` when the same GitHub repo has been connected twice, into
 * apps in different projects. Sibling apps in the same project already show
 * their kinship in the apps list; this covers the case that does not: two
 * projects, two identically named apps, one of them possibly a crashlooping
 * duplicate build.
 *
 * `twin_of` is written by the backend as
 * `{ project_id, project_name, app_name, repo_full_name }`. Any field
 * missing or the wrong type means the descriptor is untrustworthy, so this
 * returns null rather than a half-filled banner -- callers must render
 * nothing in that case, never a banner with blank fields.
 */

export interface AppTwinDescriptor {
  appName: string;
  projectId: string;
  projectName: string;
  repoFullName: string;
  /** Console route to the twin app's detail page. */
  href: string;
}

function nonEmptyString(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 ? value : null;
}

export function resolveAppTwin(twinOf: unknown): AppTwinDescriptor | null {
  if (!twinOf || typeof twinOf !== "object") return null;
  const raw = twinOf as Record<string, unknown>;

  const projectId = nonEmptyString(raw.project_id);
  const projectName = nonEmptyString(raw.project_name);
  const appName = nonEmptyString(raw.app_name);
  const repoFullName = nonEmptyString(raw.repo_full_name);

  if (!projectId || !projectName || !appName || !repoFullName) return null;

  return {
    appName,
    projectId,
    projectName,
    repoFullName,
    href: `/projects/${projectId}/apps/${appName}`,
  };
}

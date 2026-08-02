/**
 * Single source of truth for turning a build/deployment's commit fields into
 * something safe to show a user.
 *
 * Manual "Trigger build" runs get a synthetic `commit_sha` of the form
 * `manual-<timestamp>` (backend/internal/api/builds.go) written at creation
 * time, before the real HEAD commit of the branch is known. `head_sha` is
 * filled in later with the resolved real sha, or stays null when it could
 * not be resolved (or the app was deployed from an uploaded archive, which
 * has no git branch at all). Every place that renders a commit for a build
 * or deployment must go through {@link resolveCommit} instead of touching
 * `commit_sha`/`head_sha` directly, so the literal `manual-...` placeholder
 * never reaches the screen.
 */

const PLACEHOLDER_PREFIX = "manual-";

/** True only for the exact synthetic prefix written for manually triggered builds. */
export function isPlaceholderCommit(sha: string | null | undefined): boolean {
  return typeof sha === "string" && sha.startsWith(PLACEHOLDER_PREFIX);
}

export interface CommitSource {
  commit_sha?: string | null;
  commit_message?: string | null;
  head_sha?: string | null;
  branch?: string | null;
}

export type ResolvedCommit =
  | { kind: "sha"; sha: string; message?: string | null }
  | { kind: "branch"; branch: string }
  | { kind: "none" };

/**
 * Resolves what to render for a build or deployment's commit.
 *
 * - a real commit sha (either `commit_sha` when it is not the synthetic
 *   placeholder, or a resolved `head_sha`) yields `{ kind: "sha" }`, with the
 *   full sha (callers slice to whatever width they display) and message.
 * - a placeholder with no resolved `head_sha` yet yields `{ kind: "branch" }`
 *   so the caller can render an honest "latest commit on branch <branch>"
 *   instead of a fake sha.
 * - no branch either (uploaded-archive apps) yields `{ kind: "none" }`.
 */
export function resolveCommit(source: CommitSource): ResolvedCommit {
  const sha = isPlaceholderCommit(source.commit_sha) ? source.head_sha : source.commit_sha;
  if (sha) {
    return { kind: "sha", sha, message: source.commit_message ?? null };
  }
  if (source.branch) {
    return { kind: "branch", branch: source.branch };
  }
  return { kind: "none" };
}

/** Translate function shape from `useT()`, kept local to avoid importing the i18n context into this pure module. */
export type Translate = (key: string, vars?: Record<string, string | number>) => string;

/**
 * Renders a {@link ResolvedCommit} as a single plain-text label, for
 * one-line surfaces (toast notices, the AI autofix summary) that cannot lay
 * out a sha and a branch as separate elements. Uses the same `sha` (7 chars),
 * `common.commit.branchLatest`, and `common.commit.archive` phrasing as the
 * JSX call sites so the wording matches everywhere.
 */
export function formatCommitLabel(resolved: ResolvedCommit, t: Translate): string {
  if (resolved.kind === "sha") return resolved.sha.slice(0, 7);
  if (resolved.kind === "branch") return t("common.commit.branchLatest", { branch: resolved.branch });
  return t("common.commit.archive");
}

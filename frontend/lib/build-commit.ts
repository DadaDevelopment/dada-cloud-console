/**
 * Single source of truth for turning a build/deployment's commit fields into
 * something safe to show a user.
 *
 * Manual "Trigger build" runs get a synthetic `commit_sha` of the form
 * `manual-<timestamp>` (backend/internal/api/builds.go) written at creation
 * time, before the real HEAD commit of the branch is known. `head_sha` is
 * filled in later with the resolved real sha, or stays null when it could
 * not be resolved.
 *
 * Builds from an uploaded archive (no git repo at all) carry `source:
 * "archive"`. For those, `commit_sha` is a placeholder, `branch` is the
 * literal string `"upload"` (never a real git branch), and `head_sha` holds
 * an 8-char upload id derived from the archive's object key rather than a
 * git sha; `commit_message` on the first build of an upload carries the
 * original uploaded filename. `source` must be checked before the
 * placeholder/branch fallback below, otherwise `branch: "upload"` would be
 * rendered as if it were a real git branch. Every place that renders a
 * commit for a build or deployment must go through {@link resolveCommit}
 * instead of touching `commit_sha`/`head_sha`/`branch` directly, so neither
 * the literal `manual-...` placeholder nor the fake `upload` branch ever
 * reaches the screen.
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
  source?: "git" | "archive" | null;
}

export type ResolvedCommit =
  | { kind: "sha"; sha: string; message?: string | null }
  | { kind: "branch"; branch: string }
  | { kind: "archive"; uploadId?: string | null; filename?: string | null }
  | { kind: "none" };

/**
 * Resolves what to render for a build or deployment's commit.
 *
 * - `source: "archive"` yields `{ kind: "archive" }` first, before any other
 *   check -- an uploaded-archive build's `branch` is always the literal
 *   `"upload"`, never a real git branch, so it must never fall into the
 *   `"branch"` case below. `uploadId` is the human-identifiable id backfilled
 *   into `head_sha` for archive builds; `filename` is the original uploaded
 *   file name (user input -- render as text only).
 * - a real commit sha (either `commit_sha` when it is not the synthetic
 *   placeholder, or a resolved `head_sha`) yields `{ kind: "sha" }`, with the
 *   full sha (callers slice to whatever width they display) and message.
 * - a placeholder with no resolved `head_sha` yet yields `{ kind: "branch" }`
 *   so the caller can render an honest "latest commit on branch <branch>"
 *   instead of a fake sha.
 * - no branch either yields `{ kind: "none" }`. This is also the fallback for
 *   archive-ish rows that arrive without a `source` field (older rows, or
 *   callers that have not been migrated to send it yet).
 */
export function resolveCommit(source: CommitSource): ResolvedCommit {
  if (source.source === "archive") {
    return { kind: "archive", uploadId: source.head_sha ?? null, filename: source.commit_message ?? null };
  }
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
 * `common.commit.branchLatest`, `common.commit.archiveWithId`, and
 * `common.commit.archive` phrasing as the JSX call sites so the wording
 * matches everywhere.
 */
export function formatCommitLabel(resolved: ResolvedCommit, t: Translate): string {
  if (resolved.kind === "sha") return resolved.sha.slice(0, 7);
  if (resolved.kind === "branch") return t("common.commit.branchLatest", { branch: resolved.branch });
  if (resolved.kind === "archive") {
    return resolved.uploadId ? t("common.commit.archiveWithId", { id: resolved.uploadId }) : t("common.commit.archive");
  }
  return t("common.commit.archive");
}

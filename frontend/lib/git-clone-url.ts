/**
 * Parses a git clone URL into the provider + repo_full_name pair the backend's
 * ConnectGitRepo endpoint needs (backend/internal/api/gitrepos.go), so the
 * "connect by URL" form does not have to ask the user to type either one by
 * hand.
 *
 * Only https URLs are accepted. The token gets injected by the build agent as
 * an oauth2-prefixed https URL (build-agent/internal/worker/runner.go), which
 * only rewrites https URLs - an ssh URL (scp-style git@host:owner/repo.git,
 * or the ssh scheme) or a plain http URL would silently carry no auth, so
 * both are rejected here rather than accepted and failing later during a
 * build.
 *
 * A github.com host maps to provider "github"; every other host - gitlab.com
 * AND any self-hosted GitLab/Gitea/Bitbucket instance - maps to "gitlab",
 * because the build agent's non-github injection path is the same for all of
 * them.
 */

export type GitCloneUrlProvider = "github" | "gitlab";

export type ParsedGitCloneUrl = {
  provider: GitCloneUrlProvider;
  repoFullName: string;
  cloneUrl: string;
};

export type ParseGitCloneUrlError =
  | "empty"
  | "ssh-not-supported"
  | "http-not-supported"
  | "invalid-url"
  | "incomplete-path";

export type ParseGitCloneUrlResult =
  | { ok: true; value: ParsedGitCloneUrl }
  | { ok: false; error: ParseGitCloneUrlError };

const SCP_LIKE_SSH_PREFIX = /^[\w.-]+@[\w.-]+:/i;

function isSshScheme(lower: string): boolean {
  return lower.startsWith("ssh:");
}

function isHttpScheme(lower: string): boolean {
  return lower.startsWith("http:");
}

export function parseGitCloneUrl(input: string): ParseGitCloneUrlResult {
  const trimmed = input.trim();
  if (!trimmed) {
    return { ok: false, error: "empty" };
  }
  const lower = trimmed.toLowerCase();
  if (SCP_LIKE_SSH_PREFIX.test(trimmed) || isSshScheme(lower)) {
    return { ok: false, error: "ssh-not-supported" };
  }
  if (isHttpScheme(lower)) {
    return { ok: false, error: "http-not-supported" };
  }

  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    return { ok: false, error: "invalid-url" };
  }
  if (parsed.protocol !== "https:" || !parsed.hostname) {
    return { ok: false, error: "invalid-url" };
  }

  const path = parsed.pathname.replace(/^\/+|\/+$/g, "");
  const withoutGitSuffix = path.endsWith(".git") ? path.slice(0, -4) : path;
  const segments = withoutGitSuffix.split("/").filter(Boolean);
  if (segments.length < 2) {
    return { ok: false, error: "incomplete-path" };
  }

  const host = parsed.hostname.toLowerCase();
  const provider: GitCloneUrlProvider = host === "github.com" ? "github" : "gitlab";
  const repoFullName = segments.join("/");

  return {
    ok: true,
    value: { provider, repoFullName, cloneUrl: trimmed },
  };
}

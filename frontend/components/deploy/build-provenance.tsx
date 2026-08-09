"use client";

import type { Build, DeployTrigger } from "@/lib/types";
import { useT } from "@/lib/i18n/console/context";
import { formatCommitLabel, resolveCommit } from "@/lib/build-commit";
import { BuildStatusBadge, isBuildActive } from "@/components/deploy/build-status-badge";

interface BuildProvenanceProps {
  build: Build;
  /** Renders the build's status badge alongside the meta line (surfaces that show provenance outside a status-colored card). */
  showStatus?: boolean;
  className?: string;
}

/**
 * One-glance answer to "what is deployed and where did it come from" -- the
 * commit subject on top, then sha / branch / why it ran / how long ago.
 *
 * The commit subject exists because a sha alone does not tell a user that the
 * commit they just pushed is already built: production data showed users
 * pushing and then hitting the manual trigger 16-25 seconds later, whose build
 * supersedes (cancels) the push build that was already running for the same
 * commit. Naming the commit and the trigger makes the automatic build visible
 * before the user reaches for the button.
 *
 * Commit fields go through `resolveCommit` so the synthetic `manual-<ts>`
 * placeholder never reaches the screen. Timestamp prefers `finished_at` (when
 * the build actually completed) and falls back to `created_at` for rows that
 * never finished.
 */
export function BuildProvenance({ build, showStatus, className }: BuildProvenanceProps) {
  const { t } = useT();
  const resolved = resolveCommit(build);
  const subject = resolved.kind === "sha" ? firstLine(resolved.message) : resolved.kind === "archive" ? resolved.filename ?? null : null;
  const at = build.finished_at ?? build.started_at ?? build.created_at;

  return (
    <div className={className}>
      {subject ? (
        <p className="truncate text-xs text-gray-700 dark:text-gray-300" title={subject}>
          {subject}
        </p>
      ) : (
        <p className="truncate text-xs text-gray-700 dark:text-gray-300">
          {resolved.kind === "branch"
            ? t("common.commit.branchLatest", { branch: resolved.branch })
            : resolved.kind === "archive"
              ? formatCommitLabel(resolved, t)
              : t("common.commit.archive")}
        </p>
      )}
      <p className="mt-0.5 flex flex-wrap items-center gap-x-1.5 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
        {showStatus && <BuildStatusBadge status={build.status} />}
        {resolved.kind === "sha" && (
          <>
            <span className="font-mono">{resolved.sha.slice(0, 7)}</span>
            <span aria-hidden>·</span>
          </>
        )}
        {resolved.kind !== "archive" && build.branch && (
          <>
            <span className="font-mono">{build.branch}</span>
            <span aria-hidden>·</span>
          </>
        )}
        <span>{buildTriggerLabel(build.trigger, build.pr_number ?? null, t)}</span>
        <span aria-hidden>·</span>
        <span>
          {t(isBuildActive(build.status) ? "apps.build.meta.startedAgo" : "apps.build.meta.builtAgo", {
            ago: localizedAgo(at, t),
          })}
        </span>
      </p>
    </div>
  );
}

/**
 * Why this build (or deployment) ran, in the user's words. `push` is the one
 * that matters: it is the proof that auto-deploy already picked the commit up,
 * which is exactly what a user about to press the manual trigger needs to read.
 * Exported so the deployments feed labels its rows with the same wording
 * instead of printing the raw enum value.
 */
export function buildTriggerLabel(
  trigger: DeployTrigger,
  prNumber: number | null,
  t: ReturnType<typeof useT>["t"],
): string {
  switch (trigger) {
    case "push":
      return t("apps.build.trigger.push");
    case "pr":
      return prNumber ? t("apps.build.trigger.prNumbered", { number: prNumber }) : t("apps.build.trigger.pr");
    case "manual":
      return t("apps.build.trigger.manual");
    case "rollback":
      return t("apps.build.trigger.rollback");
    case "promote":
      return t("apps.build.trigger.promote");
    default:
      return trigger;
  }
}

/**
 * Relative age in the console's own language. `lib/format`'s `timeAgo` hardcodes
 * English units, which reads as broken next to a Russian sentence like
 * "собрано 3h ago", so this surface counts the units itself and lets the
 * message catalog spell them.
 */
function localizedAgo(dateStr: string, t: ReturnType<typeof useT>["t"]): string {
  const secs = Math.max(0, Math.floor((Date.now() - new Date(dateStr).getTime()) / 1000));
  if (secs < 60) return t("common.time.agoSeconds", { n: secs });
  const mins = Math.floor(secs / 60);
  if (mins < 60) return t("common.time.agoMinutes", { n: mins });
  const hours = Math.floor(mins / 60);
  if (hours < 24) return t("common.time.agoHours", { n: hours });
  return t("common.time.agoDays", { n: Math.floor(hours / 24) });
}

/** Commit messages carry a body after the subject; only the subject fits one line. */
function firstLine(message: string | null | undefined): string | null {
  if (!message) return null;
  const line = message.split("\n", 1)[0].trim();
  return line || null;
}

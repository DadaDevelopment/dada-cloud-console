/**
 * How a finished-build notice must read to the person who triggered it.
 *
 * The bottom-right notice is the first — and for a user who left the build
 * page, often the only — thing read about a failed build. Until this split it
 * said "Сборка не удалась / завершилась с ошибкой" for every terminal
 * failure, which is a lie in two of the cases the backend already
 * distinguishes: a build killed by our own outage is not the user's failure,
 * and a build stopped because the app was deleted is not a failure at all.
 * Live case 2026-08-19: the notice fired four times in a row for one user
 * while the platform side was down, telling them four times that their build
 * had failed. The honest wording already existed one click deeper on the
 * build detail page (`apps.builds.fail.reason.platformError`); this brings
 * the same verdict up to where it is actually read.
 *
 * `platform` — ours: `fail_reason === "platform_error"`.
 * `appDeleted` — nobody's: the app was removed mid-build.
 * `build` — the build genuinely did not produce an image (missing Dockerfile,
 *   a failing Dockerfile, repo access), plus every unknown or absent
 *   `fail_reason`. Unknown falls here on purpose: claiming "our fault" without
 *   the backend saying so is the same guess in the other direction.
 */
export type BuildNoticeKind = "platform" | "appDeleted" | "build";

export function classifyBuildNotice(failReason?: string | null): BuildNoticeKind {
  switch (failReason) {
    case "platform_error":
      return "platform";
    case "app_deleted":
      return "appDeleted";
    default:
      return "build";
  }
}

/** Message keys for the in-console panel, keyed by the kind above. */
export function buildNoticeFailureKeys(kind: BuildNoticeKind): { title: string; body: string } {
  return {
    title: `buildWatcher.failure.${kind}.title`,
    body: `buildWatcher.failure.${kind}.body`,
  };
}

/** Message keys for the native (background-tab) notification. */
export function buildNoticeNotifyFailureKeys(kind: BuildNoticeKind): { title: string; body: string } {
  return {
    title: `buildWatcher.notify.failure.${kind}.title`,
    body: `buildWatcher.notify.failure.${kind}.body`,
  };
}

/**
 * Telemetry target for a failure notice. The kind is part of the target so a
 * measurement can tell "the user was told we broke it" apart from "the user
 * was told their build broke" — without it, `build_notice:failure` counts an
 * outage and a missing Dockerfile as the same event.
 */
export function buildNoticeUxTarget(status: "success" | "failed", kind: BuildNoticeKind): string {
  return status === "success" ? "build_notice:success" : `build_notice:failure:${kind}`;
}

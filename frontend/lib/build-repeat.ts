/**
 * Reports whether a failed build is the second (or later) consecutive
 * failure carrying the same `fail_reason` signature.
 *
 * Live incident, 2026-08-21: `tarotreaderhimu@gmail.com` hit the same
 * `dockerfile_build_failed` three times in ten minutes -- an `npm install`
 * that was never going to pass on retry -- and in between attempts created a
 * new database, chasing a cause the failure had nothing to do with. The card
 * showed the identical red line on attempt one and attempt three with no
 * hint that repeating the same action was the mistake. `repeat_count` is the
 * strongest signal available that a person is stuck, and until now nothing
 * read it.
 *
 * @param repeatCount - `Build.repeat_count`; absent on older backends and on
 *   any non-failed build, in which case this returns false
 */
export function isStuckOnRepeat(repeatCount?: number | null): boolean {
  return typeof repeatCount === "number" && repeatCount >= 2;
}

/**
 * Picks the i18n key for the addressed next step shown once a failure is
 * repeating, keyed by the same `fail_reason` values the failed-build card
 * already switches on (see `frontend/lib/build-failure.ts` and
 * `frontend/components/deploy/app-latest-build-card.tsx::failReasonFor`).
 *
 * Each reason gets its own hint because "retry" is the wrong advice for all
 * three known repeatable classes, each for a different reason: a dependency
 * manifest problem does not fix itself on retry, a broken git link does not
 * either, and a platform-side failure is not something the user can act on
 * at all. Anything outside those three classes falls back to a generic
 * "look at the log, retrying will not change it" hint rather than inventing
 * a fail_reason that does not exist on the backend.
 *
 * @param failReason - the build's `fail_reason`, if the API returned one
 */
export function repeatHintKey(failReason?: string | null): string {
  switch (failReason) {
    case "dockerfile_build_failed":
      return "apps.latestBuild.failed.repeatHint.dockerfileBuildFailed";
    case "missing_manifest":
      return "apps.latestBuild.failed.repeatHint.missingManifest";
    case "git_auth_failed":
      return "apps.latestBuild.failed.repeatHint.gitAuthFailed";
    case "platform_error":
      return "apps.latestBuild.failed.repeatHint.platformError";
    default:
      return "apps.latestBuild.failed.repeatHint.generic";
  }
}

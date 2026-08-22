import type { DeployTrigger } from "@/lib/types";

/**
 * Reports whether a successful build was queued by the platform's class-fix
 * sweeper (backend `build_classfix_sweeper.go`) rather than by the user.
 *
 * Live incident, 2026-08-22: the sweeper's first real firing re-queued
 * `tarotreaderhimu@gmail.com`'s app after a platform-side template bug was
 * fixed, and it succeeded -- but nothing on the app-latest-build card told
 * the user that happened. A user who has already concluded the platform is
 * broken sees only a new green build with no explanation, unless this reads
 * `trigger === "class_fix"` and says so.
 *
 * @param trigger - `Build.trigger`
 */
export function isClassFixBuild(trigger: DeployTrigger | string | null | undefined): boolean {
  return trigger === "class_fix";
}

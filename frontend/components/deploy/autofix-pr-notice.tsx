"use client";

import { useEffect, useState } from "react";
import { cloudTasksApi } from "@/lib/api";
import { useT } from "@/lib/i18n/console/context";
import { trackUxEvent } from "@/lib/ux-telemetry";
import { selectAutofixPrs, type AutofixPr } from "@/lib/autofix-pr";

/**
 * Names the pull request the auto-fix agent already opened, on the surfaces a
 * user with a failing build actually looks at.
 *
 * Until this existed the agent's only output channel was `pr_url` on its cloud
 * task, read by exactly one component: the runtime-crash banner on the app
 * page. That banner needs the app to have started at least once, so a build
 * that never produced a running container hid the fix completely. freitorsk
 * (2026-09-12, `chirping-kolyaska`) is the measured cost: PR #1 at 10:49, then
 * three manual rebuilds of the same commit, then DeleteApp at 15:02 -- the
 * console never once said a fix was waiting.
 *
 * Renders nothing when there is no PR, while loading, or when the read fails:
 * this is an extra channel for a failure the user is already looking at, never
 * a second error stacked on the first.
 */
export function AutofixPrNotice({
  projectId,
  envId,
  appName,
  surface,
  className = "",
}: {
  projectId: string;
  envId: string;
  appName: string;
  surface: string;
  className?: string;
}) {
  const { t } = useT();
  const [prs, setPrs] = useState<AutofixPr[]>([]);

  useEffect(() => {
    if (!projectId || !envId || !appName) return;
    let cancelled = false;
    cloudTasksApi
      .list(projectId, envId, appName)
      .then((res) => {
        if (cancelled) return;
        setPrs(selectAutofixPrs(res.cloud_tasks));
      })
      .catch(() => {
        if (!cancelled) setPrs([]);
      });
    return () => {
      cancelled = true;
    };
  }, [projectId, envId, appName]);

  /**
   * Reports one `view` per mount that actually shows a PR, so the conversion
   * this component exists to create -- saw the PR, opened it -- is measurable
   * against the rebuild loop it replaces. Guarded on a non-empty list so an
   * app with no auto-fix history never inflates the denominator.
   */
  useEffect(() => {
    if (prs.length === 0) return;
    trackUxEvent("view", `autofix_pr_notice:${surface}`, { count: prs.length });
  }, [prs.length, surface]);

  if (prs.length === 0) return null;

  return (
    <div
      className={`rounded-lg border border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/30 px-4 py-3 ${className}`}
    >
      <p className="text-sm font-semibold text-blue-900 dark:text-blue-200">{t("apps.autofixPr.heading")}</p>
      <p className="mt-0.5 text-xs text-blue-800 dark:text-blue-300">{t("apps.autofixPr.hint")}</p>
      <div className="mt-2 space-y-1">
        {prs.map((pr) => (
          <a
            key={pr.id}
            href={pr.url}
            target="_blank"
            rel="noreferrer"
            data-ux={`autofix_pr_notice:${surface}:open_pr`}
            onClick={() => trackUxEvent("click", `autofix_pr_notice:${surface}:open_pr`)}
            className="block break-all text-xs font-semibold text-blue-700 dark:text-blue-300 underline underline-offset-2 hover:text-blue-900 dark:hover:text-blue-100"
          >
            {pr.url}
          </a>
        ))}
      </div>
    </div>
  );
}

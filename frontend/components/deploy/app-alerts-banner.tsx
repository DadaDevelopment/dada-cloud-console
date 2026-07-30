"use client";

import Link from "next/link";
import { timeAgo } from "@/lib/format";
import { useT } from "@/lib/i18n/console/context";
import type { AppAlert } from "@/lib/app-alerts";

/**
 * Maps the watcher's raw container reason to the message key, so an
 * out-of-memory kill and a failed image pull do not both read as a generic
 * crash loop. An unknown or empty reason (a cooldown row written before the
 * reason column shipped) falls back to the generic crash wording.
 */
function crashTextKey(reason?: string): string {
  switch (reason) {
    case "OOMKilled":
      return "apps.alerts.crash.text.oom";
    case "ImagePullBackOff":
    case "ErrImagePull":
      return "apps.alerts.crash.text.image";
    default:
      return "apps.alerts.crash.text";
  }
}

interface AppAlertsBannerProps {
  alerts: AppAlert[];
  logsHref: string;
  storageHref: string;
}

/**
 * Per-app alert banner: one row per alert (crash = red, volume = amber),
 * each with a plain-language reason and a link to the tab where the user can
 * act on it. Renders nothing when `alerts` is empty or absent.
 */
export function AppAlertsBanner({ alerts, logsHref, storageHref }: AppAlertsBannerProps) {
  const { t } = useT();
  if (alerts.length === 0) return null;

  return (
    <div className="mb-6 space-y-3">
      {alerts.map((alert, idx) =>
        alert.type === "crash" ? (
          <div
            key={`crash-${idx}`}
            className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300"
          >
            <div className="flex flex-wrap items-center justify-between gap-2">
              <p className="font-medium">{t(crashTextKey(alert.reason))}</p>
              <span className="text-xs text-red-500 dark:text-red-400">{timeAgo(alert.detected_at)}</span>
            </div>
            <Link
              href={logsHref}
              className="mt-1.5 inline-flex items-center gap-1 text-xs font-semibold text-red-700 dark:text-red-300 underline underline-offset-2 hover:text-red-800 dark:hover:text-red-200"
            >
              {t("apps.alerts.crash.cta")}
            </Link>
          </div>
        ) : (
          <div
            key={`volume-${idx}`}
            className="rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-700 dark:text-amber-300"
          >
            <div className="flex flex-wrap items-center justify-between gap-2">
              <p className="font-medium">
                {t("apps.alerts.volume.text", {
                  percent: alert.ratio != null ? Math.round(alert.ratio * 100) : "?",
                })}
              </p>
              <span className="text-xs text-amber-600 dark:text-amber-400">{timeAgo(alert.detected_at)}</span>
            </div>
            <Link
              href={storageHref}
              className="mt-1.5 inline-flex items-center gap-1 text-xs font-semibold text-amber-700 dark:text-amber-300 underline underline-offset-2 hover:text-amber-800 dark:hover:text-amber-200"
            >
              {t("apps.alerts.volume.cta")}
            </Link>
          </div>
        ),
      )}
    </div>
  );
}

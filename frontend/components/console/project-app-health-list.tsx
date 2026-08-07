"use client";

import Link from "next/link";
import { useEffect, useRef } from "react";
import type { ResourceSnapshot } from "@/lib/types";
import { getAppAlerts, type AppAlert } from "@/lib/app-alerts";
import { useT } from "@/lib/i18n/console/context";
import { trackUxEvent } from "@/lib/ux-telemetry";

const CRASHED_PHASES = new Set(["crashloop", "failed"]);

/**
 * True when an app's own detail page would show a red alert banner or a red
 * phase badge. Alerts win over phase: a `phase` snapshot can lag a live
 * cooldown row by a sync cycle, so a firing alert is trusted first.
 */
function isUnhealthy(app: ResourceSnapshot, alerts: AppAlert[]): boolean {
  if (alerts.length > 0) return true;
  return CRASHED_PHASES.has((app.phase ?? "").toLowerCase());
}

function reasonKey(app: ResourceSnapshot, alerts: AppAlert[]): string {
  const crash = alerts.find((a) => a.type === "crash");
  if (crash) {
    switch (crash.reason) {
      case "OOMKilled":
        return "overview.health.reason.oom";
      case "ImagePullBackOff":
      case "ErrImagePull":
        return "overview.health.reason.image";
      default:
        return "overview.health.reason.crash";
    }
  }
  if (alerts.some((a) => a.type === "volume")) return "overview.health.reason.volume";
  if ((app.phase ?? "").toLowerCase() === "failed") return "overview.health.reason.failed";
  return "overview.health.reason.crash";
}

interface Row {
  app: ResourceSnapshot;
  alerts: AppAlert[];
  unhealthy: boolean;
}

interface ProjectAppHealthListProps {
  apps: ResourceSnapshot[];
  projectId: string;
}

/**
 * Project-overview app list, sorted unhealthy-first, so a crash loop is
 * visible without clicking into each app to find it (the crash banner used to
 * live only inside the per-app page). Emits one `view` UX event per mount
 * when at least one app is unhealthy, so the fix's effect is measurable
 * against the click-through this row's `data-ux` marker already records.
 */
export function ProjectAppHealthList({ apps, projectId }: ProjectAppHealthListProps) {
  const { t } = useT();
  const viewedRef = useRef(false);

  const rows: Row[] = apps.map((app) => {
    const alerts = getAppAlerts(app);
    return { app, alerts, unhealthy: isUnhealthy(app, alerts) };
  });
  const unhealthyCount = rows.filter((r) => r.unhealthy).length;
  const sorted = [...rows].sort((a, b) => {
    if (a.unhealthy !== b.unhealthy) return a.unhealthy ? -1 : 1;
    return a.app.name.localeCompare(b.app.name);
  });

  useEffect(() => {
    if (viewedRef.current) return;
    if (unhealthyCount === 0) return;
    viewedRef.current = true;
    trackUxEvent("view", `project_app_health:${unhealthyCount}`);
  }, [unhealthyCount]);

  if (apps.length === 0) return null;

  return (
    <div className="mb-8 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("overview.health.title")}</h2>
        {unhealthyCount > 0 && (
          <span className="text-xs font-medium text-red-600 dark:text-red-400">
            {t("overview.health.unhealthyCount", { count: unhealthyCount })}
          </span>
        )}
      </div>
      <ul className="space-y-1.5">
        {sorted.map(({ app, alerts, unhealthy }) => (
          <li key={app.name}>
            <Link
              href={`/projects/${projectId}/apps/${app.name}`}
              data-ux={unhealthy ? "project_app_health:unhealthy_row" : "project_app_health:healthy_row"}
              className={
                unhealthy
                  ? "flex items-center justify-between gap-3 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-3 py-2.5 transition-colors hover:border-red-300 dark:hover:border-red-800"
                  : "flex items-center justify-between gap-3 rounded-lg border border-transparent px-3 py-2.5 transition-colors hover:bg-gray-50 dark:hover:bg-gray-800"
              }
            >
              <span className="flex min-w-0 items-center gap-2.5">
                <span
                  className={`h-2 w-2 shrink-0 rounded-full ${unhealthy ? "bg-red-500" : "bg-green-500"}`}
                  aria-hidden
                />
                <span className="min-w-0">
                  <span
                    className={`block truncate text-sm font-medium ${
                      unhealthy ? "text-red-800 dark:text-red-200" : "text-gray-900 dark:text-gray-100"
                    }`}
                  >
                    {app.name}
                  </span>
                  {unhealthy && (
                    <span className="block text-xs text-red-600 dark:text-red-400">{t(reasonKey(app, alerts))}</span>
                  )}
                </span>
              </span>
              <span
                className={`shrink-0 text-xs font-semibold ${
                  unhealthy ? "text-red-700 dark:text-red-300" : "text-gray-400 dark:text-gray-500"
                }`}
              >
                {unhealthy ? t("overview.health.view") : app.phase || t("overview.health.unknown")}
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}

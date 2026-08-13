"use client";

import Link from "next/link";
import { useState } from "react";
import { timeAgo } from "@/lib/format";
import { useT } from "@/lib/i18n/console/context";
import { Spinner } from "@/components/ui/spinner";
import { diagnoseApi, cloudTasksApi } from "@/lib/api";
import type { AppAlert } from "@/lib/app-alerts";
import type { AppDiagnosis } from "@/lib/types";

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

/**
 * Maps the backend's crash `cause_kind` to the verdict message key. Only
 * `app_code` blames the user's own code; the platform kinds say plainly that
 * the failure was on our side, and `resource_limit` (OOMKilled) says plainly
 * that the container hit its plan's memory ceiling — neither the user's
 * fault nor a platform bug, so it renders with the same neutral styling as
 * the platform kinds, never the accusatory app_code styling. An unknown or
 * absent kind returns null so the banner prints no verdict line at all,
 * rather than defaulting to "your code" without the backend having said so.
 */
function crashCauseKey(kind?: string): string | null {
  switch (kind) {
    case "app_code":
      return "apps.alerts.crash.cause.appCode";
    case "platform_network":
      return "apps.alerts.crash.cause.platformNetwork";
    case "platform_storage":
      return "apps.alerts.crash.cause.platformStorage";
    case "platform_registry":
      return "apps.alerts.crash.cause.platformRegistry";
    case "resource_limit":
      return "apps.alerts.crash.cause.resourceLimit";
    case "app_needs_args":
      return "apps.alerts.crash.cause.needsArgs";
    default:
      return null;
  }
}

/**
 * Maps the URL watcher's reason to the message key: `no_listener` means the
 * app never accepted the connection (bot/worker not listening on the port at
 * all), `not_http` means the port answered but the response was not an HTTP
 * response (a non-HTTP protocol such as MTProto behind the public domain).
 * An unknown or empty reason falls back to the generic "not a web service"
 * wording.
 */
function urlTextKey(reason?: string): string {
  switch (reason) {
    case "no_listener":
      return "apps.alerts.url.text.noListener";
    case "not_http":
      return "apps.alerts.url.text.notHttp";
    default:
      return "apps.alerts.url.text";
  }
}

type DiagnoseState =
  | { status: "idle" }
  | { status: "pending" }
  | { status: "error"; message: string }
  | { status: "done"; result: AppDiagnosis };

type AutofixState =
  | { status: "idle" }
  | { status: "pending" }
  | { status: "error"; message: string }
  | { status: "done"; prUrl?: string };

interface AppAlertsBannerProps {
  alerts: AppAlert[];
  logsHref: string;
  storageHref: string;
  projectId: string;
  envId: string;
  appName: string;
}

/**
 * Per-app alert banner: one row per alert (crash = red, volume = amber),
 * each with a plain-language reason and a link to the tab where the user can
 * act on it. The crash row also offers an inline "Diagnose" flow backed by
 * the diagnose endpoint (LLM-read logs), with an honest pending state and
 * a follow-up autofix action once a diagnosis names a fixable cause.
 * Renders nothing when `alerts` is empty or absent.
 */
export function AppAlertsBanner({ alerts, logsHref, storageHref, projectId, envId, appName }: AppAlertsBannerProps) {
  const { t } = useT();
  const [diagnose, setDiagnose] = useState<DiagnoseState>({ status: "idle" });
  const [autofix, setAutofix] = useState<AutofixState>({ status: "idle" });

  if (alerts.length === 0) return null;

  async function handleDiagnose() {
    setDiagnose({ status: "pending" });
    setAutofix({ status: "idle" });
    try {
      const result = await diagnoseApi.run(projectId, envId, appName);
      setDiagnose({ status: "done", result });
    } catch (err) {
      setDiagnose({
        status: "error",
        message: err instanceof Error ? err.message : t("apps.alerts.crash.diagnose.error"),
      });
    }
  }

  async function handleAutofix() {
    setAutofix({ status: "pending" });
    try {
      const summary = diagnose.status === "done" ? diagnose.result.diagnosis : "";
      const res = await cloudTasksApi.triggerAutofix(projectId, envId, appName, summary);
      setAutofix({ status: "done", prUrl: res.cloud_task.pr_url });
    } catch (err) {
      setAutofix({
        status: "error",
        message: err instanceof Error ? err.message : t("apps.alerts.crash.autofix.error"),
      });
    }
  }

  return (
    <div className="mb-6 space-y-3">
      {alerts.map((alert, idx) =>
        alert.type === "crash" ? (
          <div key={`crash-${idx}`} className="space-y-2">
            <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <p className="font-medium">{t(crashTextKey(alert.reason))}</p>
                <span className="text-xs text-red-500 dark:text-red-400">{timeAgo(alert.detected_at)}</span>
              </div>
              {(alert.cause || alert.cause_line) && (
                <div className="mt-2 space-y-1.5">
                  {alert.cause &&
                    crashCauseKey(alert.cause_kind) &&
                    (alert.cause_kind === "platform_network" ||
                    alert.cause_kind === "platform_storage" ||
                    alert.cause_kind === "platform_registry" ||
                    alert.cause_kind === "app_needs_args" ||
                    alert.cause_kind === "resource_limit" ? (
                      <p className="text-xs font-semibold text-red-800 dark:text-red-200">
                        {t(crashCauseKey(alert.cause_kind)!)}
                      </p>
                    ) : (
                      <p className="text-xs">{t(crashCauseKey(alert.cause_kind)!)}</p>
                    ))}
                  {alert.cause_line && (
                    <div className="overflow-x-auto rounded-md bg-red-100/70 dark:bg-red-950/60 px-2.5 py-1.5">
                      <p className="text-[11px] font-semibold uppercase tracking-wide text-red-500 dark:text-red-400">
                        {t("apps.alerts.crash.cause.line")}
                      </p>
                      <pre className="mt-0.5 whitespace-pre text-xs font-mono text-red-800 dark:text-red-200">
                        {alert.cause_line}
                      </pre>
                    </div>
                  )}
                </div>
              )}
              <div className="mt-2 flex flex-wrap items-center gap-3">
                <button
                  type="button"
                  onClick={handleDiagnose}
                  disabled={diagnose.status === "pending"}
                  className="inline-flex items-center gap-1.5 rounded-md bg-red-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-red-700 disabled:opacity-60"
                >
                  {diagnose.status === "pending" && <Spinner size="sm" />}
                  {diagnose.status === "pending" ? t("apps.alerts.crash.diagnose.pending") : t("apps.alerts.crash.diagnose")}
                </button>
                <Link
                  href={logsHref}
                  className="inline-flex items-center gap-1 text-xs font-semibold text-red-700 dark:text-red-300 underline underline-offset-2 hover:text-red-800 dark:hover:text-red-200"
                >
                  {t("apps.alerts.crash.cta")}
                </Link>
              </div>
            </div>

            {diagnose.status === "error" && (
              <div className="rounded-lg border border-red-200 dark:border-red-900 bg-white dark:bg-gray-900 px-4 py-3 text-sm text-red-700 dark:text-red-300">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span>{diagnose.message}</span>
                  <button
                    type="button"
                    onClick={handleDiagnose}
                    className="text-xs font-semibold underline underline-offset-2 hover:text-red-800 dark:hover:text-red-200"
                  >
                    {t("apps.alerts.crash.diagnose.retry")}
                  </button>
                </div>
              </div>
            )}

            {diagnose.status === "done" && (
              <div className="rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-4 py-3 text-sm text-gray-700 dark:text-gray-300 overflow-x-hidden">
                <p className="whitespace-pre-wrap break-words">{diagnose.result.diagnosis}</p>

                <details className="mt-3">
                  <summary className="cursor-pointer text-xs font-semibold text-gray-500 dark:text-gray-400">
                    {t("apps.alerts.crash.diagnose.logsTitle")}
                  </summary>
                  <div className="mt-2 max-h-64 overflow-y-auto overflow-x-auto rounded-md bg-gray-950 px-3 py-2">
                    <pre className="whitespace-pre text-xs text-gray-100 font-mono">
                      {diagnose.result.log_excerpt.join("\n")}
                    </pre>
                  </div>
                </details>

                {diagnose.result.can_autofix && (
                  <div className="mt-3 flex flex-wrap items-center gap-3">
                    <button
                      type="button"
                      onClick={handleAutofix}
                      disabled={autofix.status === "pending"}
                      className="inline-flex items-center gap-1.5 rounded-md border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-xs font-semibold text-gray-700 dark:text-gray-200 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-60"
                    >
                      {autofix.status === "pending" && <Spinner size="sm" />}
                      {autofix.status === "pending" ? t("apps.alerts.crash.autofix.pending") : t("apps.alerts.crash.autofix")}
                    </button>
                    {autofix.status === "done" && (
                      <span className="text-xs text-gray-500 dark:text-gray-400">
                        {t("apps.alerts.crash.autofix.created")}
                        {autofix.prUrl && (
                          <>
                            {" · "}
                            <a
                              href={autofix.prUrl}
                              target="_blank"
                              rel="noreferrer"
                              className="font-semibold underline underline-offset-2"
                            >
                              {t("apps.alerts.crash.autofix.prLink")}
                            </a>
                          </>
                        )}
                      </span>
                    )}
                    {autofix.status === "error" && (
                      <span className="text-xs text-red-600 dark:text-red-400">{autofix.message}</span>
                    )}
                  </div>
                )}
              </div>
            )}
          </div>
        ) : alert.type === "volume" ? (
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
        ) : (
          <div
            key={`url-${idx}`}
            className="rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-700 dark:text-amber-300"
          >
            <div className="flex flex-wrap items-center justify-between gap-2">
              <p className="font-medium">{t(urlTextKey(alert.reason))}</p>
              <span className="text-xs text-amber-600 dark:text-amber-400">{timeAgo(alert.detected_at)}</span>
            </div>
            {alert.detail && (
              <div className="mt-2 overflow-x-auto rounded-md bg-amber-100/70 dark:bg-amber-950/60 px-2.5 py-1.5">
                <p className="text-[11px] font-semibold uppercase tracking-wide text-amber-500 dark:text-amber-400">
                  {t("apps.alerts.url.detail")}
                </p>
                <pre className="mt-0.5 whitespace-pre text-xs font-mono text-amber-800 dark:text-amber-200">
                  {alert.detail}
                </pre>
              </div>
            )}
            <Link
              href={logsHref}
              className="mt-1.5 inline-flex items-center gap-1 text-xs font-semibold text-amber-700 dark:text-amber-300 underline underline-offset-2 hover:text-amber-800 dark:hover:text-amber-200"
            >
              {t("apps.alerts.url.cta")}
            </Link>
          </div>
        ),
      )}
    </div>
  );
}

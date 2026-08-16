"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { timeAgo } from "@/lib/format";
import { useT } from "@/lib/i18n/console/context";
import { Spinner } from "@/components/ui/spinner";
import { diagnoseApi, cloudTasksApi, databasesApi, envVarsApi } from "@/lib/api";
import {
  missingEnvVarKey,
  parseBadConnCauseLine,
  suggestSSLModeDisable,
  type AppAlert,
} from "@/lib/app-alerts";
import type { AppDiagnosis, ResourceSnapshot } from "@/lib/types";

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
 *
 * `db_read_only` also renders with the neutral styling: it names our own
 * quota enforcement (a managed database put into read-only mode after
 * crossing the plan's storage limit) as the cause, never the app's code.
 *
 * `bad_connection_string` deliberately returns null here too, even though it
 * IS a recognized kind: its message names a live env var key and value the
 * backend only knows at alert time, which a static translation key cannot
 * carry. The render below handles that kind with its own block that reads
 * `alert.cause_line` (see parseBadConnCauseLine) and interpolates the
 * translated template instead.
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
    case "db_read_only":
      return "apps.alerts.crash.cause.dbReadOnly";
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

type BadConnDbListState =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "loaded"; databases: ResourceSnapshot[] };

type BadConnRepairState =
  | { status: "idle" }
  | { status: "pending" }
  | { status: "error"; message: string }
  | { status: "done" };

/**
 * The one-click fix for a `bad_connection_string` alert: fetches the app's
 * environment's managed databases, and if there is exactly one, offers a
 * button that pulls its real DSN (GetDatabaseCredentials, the same
 * reveal-credentials endpoint the database page already uses) and writes it
 * into the env var named in `causeLine` (SetEnvVar, same as the manual env
 * editor) -- both endpoints already exist, already routed, already audited.
 *
 * Candidate databases are resolved by ENVIRONMENT, never by the database's
 * `appRef`: the console's own snapshot of a ServiceDatabaseV2's appRef is a
 * renderer default (falls back to the database's own name whenever no real
 * binding was set — gitops-agent/internal/renderer/renderer.go), not proof of
 * a binding, and both existing appRef-gated seed paths in the backend have
 * accordingly never once fired in production (zero SeedDatabaseDSN audit
 * rows, cluster-wide, ever). Environment-scoped listing has no such false
 * signal to trust.
 *
 * Three branches, matching how many databases the environment actually has:
 * zero -> renders nothing (no DSN to offer); exactly one -> a labelled
 * repair button; more than one -> a link to the databases page instead of a
 * guess, since nothing here can know which one the app actually wants.
 */
function BadConnDbRepair({
  projectId,
  envId,
  appName,
  causeLine,
}: {
  projectId: string;
  envId: string;
  appName: string;
  causeLine?: string;
}) {
  const { t } = useT();
  const parsed = parseBadConnCauseLine(causeLine);
  const [dbList, setDbList] = useState<BadConnDbListState>({ status: "loading" });
  const [repair, setRepair] = useState<BadConnRepairState>({ status: "idle" });

  useEffect(() => {
    let cancelled = false;
    databasesApi
      .list(projectId, envId)
      .then((res) => {
        if (!cancelled) setDbList({ status: "loaded", databases: res.databases });
      })
      .catch((err) => {
        if (!cancelled) {
          setDbList({
            status: "error",
            message: err instanceof Error ? err.message : t("apps.alerts.crash.cause.badConn.repair.listError"),
          });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [projectId, envId, t]);

  if (!parsed) return null;

  async function handleRepair(dbName: string) {
    setRepair({ status: "pending" });
    try {
      const creds = await databasesApi.credentials(projectId, envId, dbName);
      if (!creds.dsn) {
        setRepair({ status: "error", message: t("apps.alerts.crash.cause.badConn.repair.noDsn") });
        return;
      }
      await envVarsApi.upsert(projectId, envId, appName, parsed!.key, {
        value: creds.dsn,
        is_secret: true,
        scope: "runtime",
      });
      setRepair({ status: "done" });
    } catch (err) {
      setRepair({
        status: "error",
        message: err instanceof Error ? err.message : t("apps.alerts.crash.cause.badConn.repair.error"),
      });
    }
  }

  if (dbList.status === "loading") {
    return (
      <p className="text-xs text-red-500 dark:text-red-400">
        {t("apps.alerts.crash.cause.badConn.repair.checking")}
      </p>
    );
  }
  if (dbList.status === "error") {
    return <p className="text-xs text-red-600 dark:text-red-400">{dbList.message}</p>;
  }
  if (dbList.databases.length === 0) {
    return null;
  }
  if (repair.status === "done") {
    return (
      <p className="text-xs font-semibold text-red-800 dark:text-red-200">
        {t("apps.alerts.crash.cause.badConn.repair.done")}
      </p>
    );
  }
  if (dbList.databases.length > 1) {
    return (
      <Link
        href={`/projects/${projectId}/databases${envId ? `?envId=${envId}` : ""}`}
        className="inline-flex items-center gap-1 text-xs font-semibold text-red-700 dark:text-red-300 underline underline-offset-2 hover:text-red-800 dark:hover:text-red-200"
      >
        {t("apps.alerts.crash.cause.badConn.repair.chooseCta")}
      </Link>
    );
  }

  const db = dbList.databases[0];
  return (
    <div className="flex flex-wrap items-center gap-2">
      <button
        type="button"
        onClick={() => handleRepair(db.name)}
        disabled={repair.status === "pending"}
        className="inline-flex items-center gap-1.5 rounded-md bg-red-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-red-700 disabled:opacity-60"
      >
        {repair.status === "pending" && <Spinner size="sm" />}
        {t("apps.alerts.crash.cause.badConn.repair.cta", { db: db.name })}
      </button>
      {repair.status === "error" && <span className="text-xs text-red-600 dark:text-red-400">{repair.message}</span>}
    </div>
  );
}

type SslRepairState =
  | { status: "idle" }
  | { status: "pending" }
  | { status: "error"; message: string }
  | { status: "done" };

/**
 * The one-click fix for a `ssl_not_supported` alert: `causeLine` here is
 * just the bare env var key (see AppAlertCauseKind's doc comment on why —
 * unlike `bad_connection_string`, the DSN here can carry a real password, so
 * the backend never puts the value into cause/cause_line). The button
 * reveals the CURRENT value through the existing reveal endpoint at click
 * time, appends sslmode=disable to it with suggestSSLModeDisable (mirrors
 * the backend's notify.SuggestSSLModeDisable), and writes the result back
 * through the same SetEnvVar handle the manual env editor already uses —
 * both endpoints already exist, already routed, already audited. Nothing
 * here ever assigns the plaintext DSN to component state that could end up
 * rendered; it only ever flows from the reveal response into the upsert
 * call.
 */
function SslRepair({
  projectId,
  envId,
  appName,
  causeLine,
}: {
  projectId: string;
  envId: string;
  appName: string;
  causeLine?: string;
}) {
  const { t } = useT();
  const [repair, setRepair] = useState<SslRepairState>({ status: "idle" });
  const key = causeLine?.trim();

  if (!key) return null;

  async function handleRepair() {
    setRepair({ status: "pending" });
    try {
      const revealed = await envVarsApi.reveal(projectId, envId, appName, key!);
      const fixedValue = suggestSSLModeDisable(revealed.value);
      await envVarsApi.upsert(projectId, envId, appName, key!, {
        value: fixedValue,
        is_secret: true,
        scope: "runtime",
      });
      setRepair({ status: "done" });
    } catch (err) {
      setRepair({
        status: "error",
        message: err instanceof Error ? err.message : t("apps.alerts.crash.cause.sslNotSupported.repair.error"),
      });
    }
  }

  if (repair.status === "done") {
    return (
      <p className="text-xs font-semibold text-red-800 dark:text-red-200">
        {t("apps.alerts.crash.cause.sslNotSupported.repair.done")}
      </p>
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      <button
        type="button"
        onClick={handleRepair}
        disabled={repair.status === "pending"}
        className="inline-flex items-center gap-1.5 rounded-md bg-red-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-red-700 disabled:opacity-60"
      >
        {repair.status === "pending" && <Spinner size="sm" />}
        {t("apps.alerts.crash.cause.sslNotSupported.repair.cta", { key })}
      </button>
      {repair.status === "error" && <span className="text-xs text-red-600 dark:text-red-400">{repair.message}</span>}
    </div>
  );
}

type MissingEnvRepairState =
  | { status: "idle" }
  | { status: "pending" }
  | { status: "error"; message: string }
  | { status: "done" };

/**
 * The inline fix for a `missing_env_var` alert: the app told us in its own
 * crash log which variable it wants, and `causeLine` carries that key, so the
 * banner asks for the one thing the platform cannot know — the value — right
 * where the failure is stated, instead of sending the owner off to find the
 * settings tab and retype the name from memory.
 *
 * There is no one-click button here, unlike the `bad_connection_string` and
 * `ssl_not_supported` repairs: those two recover the value from something the
 * platform already holds (a managed database's DSN, the current env var), and
 * a bot token or API key exists only in the owner's head. Asking is the whole
 * interaction.
 *
 * The value is written with `is_secret: true` and `scope: "runtime"` through
 * the same SetEnvVar handle the manual editor uses; that endpoint queues the
 * env-apply operation itself (see the backend's queueEnvApply), so a
 * successful save is also the redeploy — nothing else has to be clicked. The
 * value is held in component state only until the request resolves and is
 * never echoed back into the DOM after the save.
 */
function MissingEnvVarRepair({
  projectId,
  envId,
  appName,
  causeLine,
}: {
  projectId: string;
  envId: string;
  appName: string;
  causeLine?: string;
}) {
  const { t } = useT();
  const [value, setValue] = useState("");
  const [repair, setRepair] = useState<MissingEnvRepairState>({ status: "idle" });
  const key = missingEnvVarKey(causeLine);

  if (!key) return null;

  async function handleSave() {
    if (!value.trim()) return;
    setRepair({ status: "pending" });
    try {
      await envVarsApi.upsert(projectId, envId, appName, key!, {
        value,
        is_secret: true,
        scope: "runtime",
      });
      setValue("");
      setRepair({ status: "done" });
    } catch (err) {
      setRepair({
        status: "error",
        message: err instanceof Error ? err.message : t("apps.alerts.crash.cause.missingEnvVar.repair.error"),
      });
    }
  }

  if (repair.status === "done") {
    return (
      <p className="text-xs font-semibold text-red-800 dark:text-red-200">
        {t("apps.alerts.crash.cause.missingEnvVar.repair.done", { key })}
      </p>
    );
  }

  return (
    <div className="space-y-1.5">
      <div className="flex flex-wrap items-center gap-2">
        <label htmlFor={`missing-env-${key}`} className="font-mono text-xs font-semibold text-red-800 dark:text-red-200">
          {key}
        </label>
        <input
          id={`missing-env-${key}`}
          type="password"
          autoComplete="off"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") handleSave();
          }}
          placeholder={t("apps.alerts.crash.cause.missingEnvVar.repair.placeholder")}
          className="min-w-0 flex-1 rounded-md border border-red-300 dark:border-red-800 bg-white dark:bg-gray-900 px-2.5 py-1.5 text-xs text-gray-900 dark:text-gray-100 placeholder:text-gray-400"
        />
        <button
          type="button"
          onClick={handleSave}
          disabled={repair.status === "pending" || !value.trim()}
          className="inline-flex items-center gap-1.5 rounded-md bg-red-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-red-700 disabled:opacity-60"
        >
          {repair.status === "pending" && <Spinner size="sm" />}
          {t("apps.alerts.crash.cause.missingEnvVar.repair.cta")}
        </button>
      </div>
      {repair.status === "error" && <p className="text-xs text-red-600 dark:text-red-400">{repair.message}</p>}
    </div>
  );
}

/**
 * Surfaces pull requests the platform's own agent opened for this app while
 * the owner was not looking. Autofix runs as a cloud task: it clones the
 * repo, commits a fix and opens a PR, and until now the only place that PR
 * ever appeared was the transient success state of the button that started
 * it — so an owner who closed the tab, or whose fix was triggered from
 * somewhere other than this banner, never learned a PR existed at all.
 *
 * Live case: a user's bot had a PR waiting on GitHub from 2026-07-24,
 * open and unread, while they deleted their projects and left. Nothing in
 * the console ever mentioned it.
 *
 * Renders nothing at all when the list is empty, still loading, or fails to
 * load: this is an extra channel for something that was already lost, never
 * an error the owner has to deal with on top of a crash.
 */
function CrashPullRequests({
  projectId,
  envId,
  appName,
}: {
  projectId: string;
  envId: string;
  appName: string;
}) {
  const { t } = useT();
  const [prs, setPrs] = useState<{ id: string; url: string }[]>([]);

  useEffect(() => {
    let cancelled = false;
    cloudTasksApi
      .list(projectId, envId, appName)
      .then((res) => {
        if (cancelled) return;
        setPrs(
          res.cloud_tasks
            .filter((task) => !!task.pr_url)
            .map((task) => ({ id: task.id, url: task.pr_url! })),
        );
      })
      .catch(() => {
        if (!cancelled) setPrs([]);
      });
    return () => {
      cancelled = true;
    };
  }, [projectId, envId, appName]);

  if (prs.length === 0) return null;

  return (
    <div className="mt-2 space-y-1">
      <p className="text-xs font-semibold text-red-800 dark:text-red-200">{t("apps.alerts.crash.openPrs")}</p>
      {prs.map((pr) => (
        <a
          key={pr.id}
          href={pr.url}
          target="_blank"
          rel="noreferrer"
          className="block break-all text-xs font-semibold text-red-700 dark:text-red-300 underline underline-offset-2 hover:text-red-800 dark:hover:text-red-200"
        >
          {pr.url}
        </a>
      ))}
    </div>
  );
}

interface AppAlertsBannerProps {
  alerts: AppAlert[];
  logsHref: string;
  storageHref: string;
  startCommandHref: string;
  envVarsHref: string;
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
export function AppAlertsBanner({ alerts, logsHref, storageHref, startCommandHref, envVarsHref, projectId, envId, appName }: AppAlertsBannerProps) {
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
                    alert.cause_kind === "resource_limit" ||
                    alert.cause_kind === "db_read_only" ? (
                      <p className="text-xs font-semibold text-red-800 dark:text-red-200">
                        {t(crashCauseKey(alert.cause_kind)!)}
                      </p>
                    ) : (
                      <p className="text-xs">{t(crashCauseKey(alert.cause_kind)!)}</p>
                    ))}
                  {alert.cause_kind === "bad_connection_string" &&
                    (() => {
                      const parsed = parseBadConnCauseLine(alert.cause_line);
                      return (
                        <>
                          <p className="text-xs font-semibold text-red-800 dark:text-red-200">
                            {parsed
                              ? t("apps.alerts.crash.cause.badConn", { key: parsed.key, value: parsed.value })
                              : alert.cause}
                          </p>
                          <BadConnDbRepair
                            projectId={projectId}
                            envId={envId}
                            appName={appName}
                            causeLine={alert.cause_line}
                          />
                        </>
                      );
                    })()}
                  {alert.cause_kind === "ssl_not_supported" && (
                    <>
                      <p className="text-xs font-semibold text-red-800 dark:text-red-200">
                        {t("apps.alerts.crash.cause.sslNotSupported", { key: alert.cause_line ?? "" })}
                      </p>
                      <SslRepair
                        projectId={projectId}
                        envId={envId}
                        appName={appName}
                        causeLine={alert.cause_line}
                      />
                    </>
                  )}
                  {alert.cause_kind === "missing_env_var" && (
                    <>
                      <p className="text-xs font-semibold text-red-800 dark:text-red-200">
                        {t("apps.alerts.crash.cause.missingEnvVar", { key: alert.cause_line ?? "" })}
                      </p>
                      <MissingEnvVarRepair
                        projectId={projectId}
                        envId={envId}
                        appName={appName}
                        causeLine={alert.cause_line}
                      />
                    </>
                  )}
                  {alert.cause_line &&
                    alert.cause_kind !== "bad_connection_string" &&
                    alert.cause_kind !== "ssl_not_supported" &&
                    alert.cause_kind !== "missing_env_var" && (
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
                {alert.cause_kind === "missing_env_var" && (
                  <Link
                    href={envVarsHref}
                    className="inline-flex items-center gap-1 text-xs font-semibold text-red-700 dark:text-red-300 underline underline-offset-2 hover:text-red-800 dark:hover:text-red-200"
                  >
                    {t("apps.alerts.crash.cause.missingEnvVar.settings")}
                  </Link>
                )}
                {alert.cause_kind === "app_needs_args" && (
                  <Link
                    href={startCommandHref}
                    className="inline-flex items-center gap-1 text-xs font-semibold text-red-700 dark:text-red-300 underline underline-offset-2 hover:text-red-800 dark:hover:text-red-200"
                  >
                    {t("apps.alerts.crash.cause.needsArgs.cta")}
                  </Link>
                )}
                {alert.cause_kind === "db_read_only" && (
                  <>
                    <Link
                      href={`/projects/${projectId}/databases${envId ? `?envId=${envId}` : ""}`}
                      className="inline-flex items-center gap-1 text-xs font-semibold text-red-700 dark:text-red-300 underline underline-offset-2 hover:text-red-800 dark:hover:text-red-200"
                    >
                      {t("apps.alerts.crash.cause.dbReadOnly.databasesCta")}
                    </Link>
                    <Link
                      href={`/projects/${projectId}/billing`}
                      className="inline-flex items-center gap-1 text-xs font-semibold text-red-700 dark:text-red-300 underline underline-offset-2 hover:text-red-800 dark:hover:text-red-200"
                    >
                      {t("apps.alerts.crash.cause.dbReadOnly.upgradeCta")}
                    </Link>
                  </>
                )}
              </div>
              <CrashPullRequests projectId={projectId} envId={envId} appName={appName} />
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

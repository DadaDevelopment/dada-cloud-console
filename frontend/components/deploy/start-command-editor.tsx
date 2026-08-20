"use client";
import { useCallback, useEffect, useState } from "react";
import { appsApi, projectsApi } from "@/lib/api";
import { saveStartCommand } from "@/lib/start-command-redeploy";
import { useT } from "@/lib/i18n/console/context";
import { Spinner } from "@/components/ui/spinner";

interface Props {
  projectId: string;
  envId: string;
  appName: string;
  canEdit: boolean;
  /**
   * When true, saving also queues an immediate redeploy of the app's current
   * image (atomically with the save, server-side) and the button waits for
   * that redeploy to reach a terminal status before it reports success. Set
   * only from the crash-banner repair flow (`?repair=start-command` on the
   * settings page) — see the settings page for why this is opt-in rather
   * than the editor's default: an unrelated config edit must never force a
   * re-render the way UpdateAppProfile once did (2026-08-02 incident, see
   * the backend UpdateAppStartCommand doc comment).
   */
  autoRedeploy?: boolean;
}

type ApplyPhase = "idle" | "applying" | "applied" | "apply-failed";

/**
 * Editable override for the shell-style arguments an app's container starts
 * with (backend field `start_command`, PATCH .../start-command). Not the values.yaml
 * WebSocket CommonConfigEditor uses — the value lives on the app row, and
 * the shared Helm chart renders it as a Docker CMD override run through a
 * shell. By default the endpoint does not enqueue a redeploy of its own; the
 * new value takes effect on the app's next deploy. With `autoRedeploy`, the
 * save also queues and awaits an immediate redeploy of the CURRENT image
 * (never a rebuild — the start command is a chart-level pod-command override,
 * independent of the image), so the crash-repair path is one action instead
 * of two. Loads the current value itself so it can be dropped into any tab
 * without the parent having to fetch the app first, and re-reads the app
 * after a save so the field always reflects what the server actually
 * persisted, not just the optimistic local edit.
 */
export function StartCommandEditor({ projectId, envId, appName, canEdit, autoRedeploy }: Props) {
  const { t } = useT();
  const [serverValue, setServerValue] = useState<string | null>(null);
  const [value, setValue] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [applyPhase, setApplyPhase] = useState<ApplyPhase>("idle");

  const load = useCallback(async () => {
    if (!envId) return;
    setLoading(true);
    try {
      const d = await appsApi.list(projectId, envId);
      const found = (d.apps ?? []).find((a) => a.name === appName);
      const sc = (found?.summary_json as { start_command?: string } | undefined)?.start_command ?? "";
      setServerValue(sc);
      setValue(sc);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("apps.startCommand.error.load"));
    } finally {
      setLoading(false);
    }
  }, [projectId, envId, appName, t]);

  useEffect(() => {
    const timer = setTimeout(() => {
      load().catch(() => undefined);
    }, 0);
    return () => clearTimeout(timer);
  }, [load]);

  /**
   * Saves through the pure saveStartCommand (lib/start-command-redeploy.ts)
   * so the optimistic-but-ACID rule -- never report success while a queued
   * redeploy is still pending or has failed -- lives in a unit-testable
   * function instead of only inside this component.
   */
  async function handleSave() {
    setSaving(true);
    setError(null);
    setSaved(false);
    setApplyPhase(autoRedeploy ? "applying" : "idle");
    try {
      const result = await saveStartCommand(
        {
          updateStartCommand: (v, redeploy) => appsApi.updateStartCommand(projectId, envId, appName, v, redeploy),
          getOperation: (opId) => projectsApi.getOperation(projectId, opId),
        },
        value,
        !!autoRedeploy
      );
      await load();
      switch (result.status) {
        case "saved":
          setApplyPhase("idle");
          setSaved(true);
          break;
        case "applied":
          setApplyPhase("applied");
          setSaved(true);
          break;
        case "apply-failed":
          setApplyPhase("apply-failed");
          setError(result.message || t("apps.startCommand.error.apply"));
          break;
        case "apply-timeout":
          setApplyPhase("apply-failed");
          setError(t("apps.startCommand.error.applyTimeout"));
          break;
      }
    } catch (err) {
      setApplyPhase("idle");
      setError(err instanceof Error ? err.message : t("apps.startCommand.error.save"));
    } finally {
      setSaving(false);
    }
  }

  const dirty = serverValue !== null && value !== serverValue;
  const applying = applyPhase === "applying";

  return (
    <div id="start-command" className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-6">
      <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("apps.startCommand.title")}</h2>
      <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("apps.startCommand.subtitle")}</p>

      {loading ? (
        <div className="mt-6 flex h-16 items-center justify-center">
          <Spinner />
        </div>
      ) : (
        <>
          <div className="mt-4">
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">{t("apps.startCommand.label")}</label>
            <input
              type="text"
              value={value}
              onChange={(e) => {
                setValue(e.target.value);
                setSaved(false);
                setApplyPhase("idle");
              }}
              disabled={!canEdit || saving || applying}
              placeholder={t("apps.startCommand.placeholder")}
              className="mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 disabled:opacity-50"
            />
            <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">
              {serverValue ? t("apps.startCommand.currentSet") : t("apps.startCommand.currentDefault")}
            </p>
          </div>

          {applying && (
            <div className="mt-3 flex items-center gap-2 rounded-lg border border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/40 px-4 py-3 text-sm text-blue-700 dark:text-blue-300">
              <Spinner size="sm" />
              {t("apps.startCommand.applying")}
            </div>
          )}

          {error && (
            <div role="alert" className="mt-3 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
              {error}
            </div>
          )}

          {saved && !error && !applying && (
            <p className="mt-3 text-sm text-green-600 dark:text-green-400">
              {applyPhase === "applied" ? t("apps.startCommand.applied") : t("apps.startCommand.saved")}
            </p>
          )}

          {canEdit && (
            <button
              onClick={handleSave}
              disabled={saving || applying || !dirty}
              className="mt-4 inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
            >
              {(saving || applying) && <Spinner size="sm" />}
              {saving
                ? t("apps.startCommand.saving")
                : applying
                  ? t("apps.startCommand.applying")
                  : t("apps.startCommand.save")}
            </button>
          )}
        </>
      )}
    </div>
  );
}

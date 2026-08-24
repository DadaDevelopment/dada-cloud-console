"use client";
import { useCallback, useEffect, useState } from "react";
import { appsApi, projectsApi } from "@/lib/api";
import { savePort } from "@/lib/port-redeploy";
import { useT } from "@/lib/i18n/console/context";
import { Spinner } from "@/components/ui/spinner";

interface Props {
  projectId: string;
  envId: string;
  appName: string;
  canEdit: boolean;
}

type ApplyPhase = "idle" | "applying" | "applied" | "apply-failed";

/**
 * Editable override for the port an app's container listens on (backend
 * field `port` on the app row, PATCH .../port). Framework autodetection
 * picks this value once at app creation and it used to be fixed forever --
 * a wrong detection (e.g. Vite's default 4173 for a process actually bound
 * to 3000) left the app on a permanent 502 with no lever inside the product.
 * Mirrors components/deploy/start-command-editor.tsx: loads the current
 * value itself, saves through the pure savePort (lib/port-redeploy.ts) so
 * the optimistic-but-ACID rule -- never report success while a queued
 * redeploy is still pending or has failed -- lives in a unit-testable
 * function, and re-reads the app after a save.
 */
export function PortEditor({ projectId, envId, appName, canEdit }: Props) {
  const { t } = useT();
  const [serverValue, setServerValue] = useState<number | null>(null);
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
      const port = (found?.summary_json as { port?: number } | undefined)?.port;
      const resolved = typeof port === "number" && Number.isFinite(port) ? port : null;
      setServerValue(resolved);
      setValue(resolved != null ? String(resolved) : "");
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("apps.port.error.load"));
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

  function validate(): number | null {
    const port = Number(value);
    if (!Number.isInteger(port) || port < 1 || port > 65535) return null;
    return port;
  }

  /**
   * Errors branch only on `err.status` / `err.code`, never on `err.message`
   * text -- see lib/env-error.ts for the same house rule against regexing
   * error prose.
   *
   * A bare 400 is NOT the range error. Reading every 400 as one is how a user
   * whose app was rejected with `app_is_worker` was told for two days that
   * 8080 is outside 1..65535: the form accused the value it had just been
   * given while the real reason went unsaid. An unrecognised 400 now shows the
   * backend's own sentence, and only `invalid_port` claims the range.
   */
  function describeError(err: unknown): string {
    const e = err as { status?: number; code?: string; message?: string } | undefined;
    if (e?.code === "invalid_port") return t("apps.port.error.invalid");
    if (e?.status === 403) return t("apps.port.error.forbidden");
    if (e?.status === 404) return t("apps.port.error.notFound");
    return e?.message || t("apps.port.error.save");
  }

  async function handleSave() {
    const port = validate();
    if (port == null) {
      setError(t("apps.port.error.invalid"));
      return;
    }
    setSaving(true);
    setError(null);
    setSaved(false);
    setApplyPhase("idle");
    try {
      const result = await savePort(
        {
          updatePort: (p) => appsApi.updatePort(projectId, envId, appName, p),
          getOperation: (opId) => projectsApi.getOperation(projectId, opId),
        },
        port
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
          setError(result.message || t("apps.port.error.apply"));
          break;
        case "apply-timeout":
          setApplyPhase("apply-failed");
          setError(t("apps.port.error.applyTimeout"));
          break;
      }
    } catch (err) {
      setApplyPhase("idle");
      setError(describeError(err));
    } finally {
      setSaving(false);
    }
  }

  const dirty = serverValue !== null && value !== String(serverValue);
  const applying = applyPhase === "applying";

  return (
    <div id="port" className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-6">
      <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("apps.port.title")}</h2>
      <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("apps.port.subtitle")}</p>

      {loading ? (
        <div className="mt-6 flex h-16 items-center justify-center">
          <Spinner />
        </div>
      ) : (
        <>
          <div className="mt-4">
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">{t("apps.port.label")}</label>
            <input
              type="number"
              min={1}
              max={65535}
              value={value}
              onChange={(e) => {
                setValue(e.target.value);
                setSaved(false);
                setApplyPhase("idle");
              }}
              disabled={!canEdit || saving || applying}
              placeholder="3000"
              className="mt-1 w-40 rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 disabled:opacity-50"
            />
          </div>

          {applying && (
            <div className="mt-3 flex items-center gap-2 rounded-lg border border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/40 px-4 py-3 text-sm text-blue-700 dark:text-blue-300">
              <Spinner size="sm" />
              {t("apps.port.applying")}
            </div>
          )}

          {error && (
            <div role="alert" className="mt-3 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
              {error}
            </div>
          )}

          {saved && !error && !applying && (
            <p className="mt-3 text-sm text-green-600 dark:text-green-400">
              {applyPhase === "applied" ? t("apps.port.applied") : t("apps.port.saved")}
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
                ? t("apps.port.saving")
                : applying
                  ? t("apps.port.applying")
                  : t("apps.port.save")}
            </button>
          )}
        </>
      )}
    </div>
  );
}

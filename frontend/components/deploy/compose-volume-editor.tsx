"use client";
import { useEffect, useState } from "react";
import { appsApi } from "@/lib/api";
import { useT } from "@/lib/i18n/console/context";
import { Spinner } from "@/components/ui/spinner";

interface Props {
  projectId: string;
  envId: string;
  appName: string;
  canEdit: boolean;
}

interface ComposeDesired {
  volumes?: string[];
  compose?: { volumes?: string[] };
}

function isNamedVolume(v: string): boolean {
  return v.includes(":") && !v.startsWith("/") && !v.startsWith(".");
}

/**
 * ComposeVolumeEditor edits the named Docker volumes of a VM (docker compose)
 * app -- source:target rows, e.g. "data:/var/lib/data". Bind mounts (a source
 * starting with "/" or ".") are rejected client-side, mirroring the backend's
 * own validation. Loads via appsApi.list, saves via
 * appsApi.updateComposeVolume; saving redeploys the compose stack and
 * preserves existing volume data.
 */
export function ComposeVolumeEditor({ projectId, envId, appName, canEdit }: Props) {
  const { t } = useT();
  const [volumes, setVolumes] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  useEffect(() => {
    if (!envId) return;
    appsApi
      .list(projectId, envId)
      .then((d) => {
        const app = (d.apps ?? []).find((a) => a.name === appName);
        const desired = app?.summary_json?.desired as ComposeDesired | undefined;
        setVolumes(desired?.compose?.volumes ?? desired?.volumes ?? []);
      })
      .catch(() => {});
  }, [projectId, envId, appName]);

  function updateVolume(i: number, v: string) {
    setVolumes((vs) => vs.map((x, idx) => (idx === i ? v : x)));
  }
  function removeVolume(i: number) {
    setVolumes((vs) => vs.filter((_, idx) => idx !== i));
  }
  function addVolume() {
    setVolumes((vs) => [...vs, ""]);
  }

  function validate(): string | null {
    for (const v of volumes) {
      const trimmed = v.trim();
      if (!trimmed) continue;
      if (!trimmed.includes(":")) return t("apps.compose.volume.invalid.format");
      if (!isNamedVolume(trimmed)) return t("apps.compose.volume.invalid.bind");
    }
    return null;
  }

  async function save() {
    const err = validate();
    if (err) {
      setMsg({ kind: "err", text: err });
      return;
    }
    setSaving(true);
    setMsg(null);
    try {
      const clean = volumes.map((v) => v.trim()).filter(Boolean);
      await appsApi.updateComposeVolume(projectId, envId, appName, { volumes: clean });
      setVolumes(clean);
      setMsg({ kind: "ok", text: t("apps.compose.volume.queued") });
    } catch (e) {
      setMsg({ kind: "err", text: e instanceof Error ? e.message : t("apps.compose.volume.error") });
    } finally {
      setSaving(false);
    }
  }

  const rowField =
    "w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 disabled:opacity-50";
  const disabled = !canEdit || saving;

  return (
    <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-6">
      <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("apps.compose.volume.title")}</h2>
      <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("apps.compose.volume.subtitle")}</p>
      <p className="mt-3 rounded-lg bg-gray-50 dark:bg-gray-950 px-4 py-3 text-xs text-gray-500 dark:text-gray-400">
        {t("apps.compose.volume.note")}
      </p>

      <div className="mt-5">
        <p className="text-xs text-gray-400">{t("apps.compose.volume.hint")}</p>
        <div className="mt-2 space-y-2">
          {volumes.length === 0 && (
            <p className="text-sm text-gray-400 dark:text-gray-500">{t("apps.compose.volume.empty")}</p>
          )}
          {volumes.map((v, i) => (
            <div key={i} className="flex items-center gap-2">
              <input
                className={rowField}
                value={v}
                onChange={(e) => updateVolume(i, e.target.value)}
                disabled={disabled}
                placeholder="data:/var/lib/data"
              />
              <button
                type="button"
                onClick={() => removeVolume(i)}
                disabled={disabled}
                aria-label={t("common.remove")}
                className="shrink-0 rounded-lg border border-gray-300 dark:border-gray-700 p-2 text-gray-500 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50"
              >
                <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>
          ))}
        </div>
        <button
          type="button"
          onClick={addVolume}
          disabled={disabled}
          className="mt-2 inline-flex items-center gap-1.5 rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50"
        >
          <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
          </svg>
          {t("common.add")}
        </button>
      </div>

      {msg && (
        <p
          className={`mt-4 text-sm ${
            msg.kind === "ok" ? "text-green-600 dark:text-green-400" : "text-red-600 dark:text-red-400"
          }`}
        >
          {msg.text}
        </p>
      )}

      <button
        onClick={save}
        disabled={disabled}
        className="mt-4 inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
      >
        {saving ? (
          <>
            <Spinner size="sm" />
            {t("apps.config.saving")}
          </>
        ) : (
          t("apps.config.save")
        )}
      </button>
    </div>
  );
}

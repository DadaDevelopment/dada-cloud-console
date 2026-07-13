"use client";
import { useEffect, useState } from "react";
import { appsApi } from "@/lib/api";
import type { AppVolume } from "@/lib/types";
import { useT } from "@/lib/i18n/console/context";

const STORAGE_CLASSES = ["longhorn-prod", "longhorn-stateful-prod"];

interface Props {
  projectId: string;
  envId: string;
  appName: string;
  canEdit: boolean;
}

/**
 * StorageManager attaches or resizes an app's persistent data directory. It reads
 * the current volume from the app's resource snapshot and submits an
 * UpdateAppStorage operation. Size may only grow and the storage class is fixed
 * once the volume exists; the form enforces both client-side to match the API.
 */
export function StorageManager({ projectId, envId, appName, canEdit }: Props) {
  const { t } = useT();
  const [current, setCurrent] = useState<AppVolume | null>(null);
  const [path, setPath] = useState("/data");
  const [size, setSize] = useState("1Gi");
  const [storageClass, setStorageClass] = useState(STORAGE_CLASSES[0]);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  useEffect(() => {
    if (!envId) return;
    appsApi
      .list(projectId, envId)
      .then((d) => {
        const app = (d.apps ?? []).find((a) => a.name === appName);
        const vol = app?.summary_json?.volume as AppVolume | undefined;
        if (vol?.path) {
          setCurrent(vol);
          setPath(vol.path);
          setSize(vol.size);
          if (vol.storage_class) setStorageClass(vol.storage_class);
        }
      })
      .catch(() => {});
  }, [projectId, envId, appName]);

  async function submit() {
    setBusy(true);
    setMsg(null);
    try {
      await appsApi.updateStorage(projectId, envId, appName, {
        path,
        size,
        storage_class: storageClass,
      });
      setCurrent({ path, size, storage_class: storageClass });
      setMsg({ kind: "ok", text: t("apps.storage.queued") });
    } catch (e) {
      setMsg({ kind: "err", text: e instanceof Error ? e.message : t("apps.storage.error") });
    } finally {
      setBusy(false);
    }
  }

  const field =
    "mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 disabled:opacity-50";
  const label = "block text-sm font-medium text-gray-700 dark:text-gray-300";

  return (
    <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-6">
      <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("apps.storage.title")}</h2>
      <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("apps.storage.subtitle")}</p>

      <div className="mt-4 rounded-lg bg-gray-50 dark:bg-gray-950 px-4 py-3 text-sm">
        <span className="text-gray-500 dark:text-gray-400">{t("apps.storage.current")}: </span>
        {current ? (
          <span className="font-mono text-gray-900 dark:text-gray-100">
            {current.path} · {current.size} · {current.storage_class}
          </span>
        ) : (
          <span className="text-gray-400 dark:text-gray-500">{t("apps.storage.none")}</span>
        )}
      </div>

      <div className="mt-5 grid gap-4 sm:grid-cols-3">
        <div className="sm:col-span-3">
          <label className={label}>{t("apps.storage.path")}</label>
          <input
            className={field}
            value={path}
            onChange={(e) => setPath(e.target.value)}
            disabled={!canEdit || busy}
            placeholder="/data"
          />
          <p className="mt-1 text-xs text-gray-400">{t("apps.storage.pathHint")}</p>
        </div>
        <div>
          <label className={label}>{t("apps.storage.size")}</label>
          <input
            className={field}
            value={size}
            onChange={(e) => setSize(e.target.value)}
            disabled={!canEdit || busy}
            placeholder="1Gi"
          />
          <p className="mt-1 text-xs text-gray-400">{t("apps.storage.sizeHint")}</p>
        </div>
        <div className="sm:col-span-2">
          <label className={label}>{t("apps.storage.storageClass")}</label>
          <select
            className={field}
            value={storageClass}
            onChange={(e) => setStorageClass(e.target.value)}
            disabled={!canEdit || busy || !!current}
          >
            {STORAGE_CLASSES.map((sc) => (
              <option key={sc} value={sc}>
                {sc}
              </option>
            ))}
          </select>
        </div>
      </div>

      <p className="mt-3 text-xs text-amber-600 dark:text-amber-500">{t("apps.storage.warnImmutable")}</p>

      {msg && (
        <p
          className={`mt-3 text-sm ${
            msg.kind === "ok" ? "text-green-600 dark:text-green-400" : "text-red-600 dark:text-red-400"
          }`}
        >
          {msg.text}
        </p>
      )}

      <button
        onClick={submit}
        disabled={!canEdit || busy || !path || !size}
        className="mt-4 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
      >
        {current ? t("apps.storage.update") : t("apps.storage.attach")}
      </button>
    </div>
  );
}

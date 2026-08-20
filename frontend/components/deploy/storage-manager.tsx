"use client";
import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { appsApi, getToken, API_BASE_URL } from "@/lib/api";
import type { AppVolume, VolumeMaintenanceReport, VolumeMaintenanceTopDir } from "@/lib/types";
import { useT } from "@/lib/i18n/console/context";
import { formatBytes } from "@/components/charts/format";
import {
  evaluateVolumeUsage,
  severityBarClass,
  severityTextClass,
  formatCount,
  type VolumeUsage,
} from "@/lib/volume-usage";

const maintenancePollMs = 4_000;

/**
 * Selectable Longhorn storage classes. longhorn-dev is the 2-replica default: it
 * is the only class that reliably schedules on beget-prod, where three storage
 * nodes with strict replica anti-affinity and chronic disk pressure keep one node
 * below the schedulable floor, so a 3-replica volume cannot place its third
 * replica and never attaches. The 3-replica classes stay listed for existing
 * volumes but are labelled as such.
 */
const STORAGE_CLASSES: { value: string; label: string }[] = [
  { value: "longhorn-dev", label: "Standard (2 replicas)" },
  { value: "longhorn-prod", label: "High-redundancy (3 replicas, may not schedule)" },
  { value: "longhorn-stateful-prod", label: "Stateful (3 replicas, may not schedule)" },
];

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
  const [storageClass, setStorageClass] = useState(STORAGE_CLASSES[0].value);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);
  const [exportBusy, setExportBusy] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);
  const [exportResult, setExportResult] = useState<{ url: string; filename: string } | null>(null);
  const [usage, setUsage] = useState<VolumeUsage | null>(null);

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

  useEffect(() => {
    if (!envId || !current) return;
    let cancelled = false;
    appsApi
      .volumeUsage(projectId, envId, appName)
      .then((d) => {
        if (!cancelled) {
          setUsage({
            ratio: d.ratio,
            inodes_used: d.inodes_used,
            inodes_total: d.inodes_total,
            inodes_ratio: d.inodes_ratio,
          });
        }
      })
      .catch(() => {
        if (!cancelled) setUsage(null);
      });
    return () => {
      cancelled = true;
    };
  }, [projectId, envId, appName, current]);

  const [report, setReport] = useState<VolumeMaintenanceReport | null>(null);
  const [reportBusy, setReportBusy] = useState(false);
  const [reportError, setReportError] = useState<string | null>(null);
  const reportPollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    return () => {
      if (reportPollRef.current) clearInterval(reportPollRef.current);
    };
  }, []);

  function pollReport() {
    if (reportPollRef.current) clearInterval(reportPollRef.current);
    reportPollRef.current = setInterval(() => {
      appsApi
        .getVolumeMaintenanceReport(projectId, envId, appName)
        .then((r) => {
          setReport(r);
          if (r.status !== "running" && reportPollRef.current) {
            clearInterval(reportPollRef.current);
            reportPollRef.current = null;
          }
        })
        .catch(() => {
          if (reportPollRef.current) {
            clearInterval(reportPollRef.current);
            reportPollRef.current = null;
          }
        });
    }, maintenancePollMs);
  }

  async function startReport() {
    setReportBusy(true);
    setReportError(null);
    try {
      await appsApi.startVolumeMaintenanceReport(projectId, envId, appName);
      setReport({ status: "running" });
      pollReport();
    } catch (e) {
      setReportError(e instanceof Error ? e.message : t("apps.storage.report.startError"));
    } finally {
      setReportBusy(false);
    }
  }

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

  async function exportVolume() {
    setExportBusy(true);
    setExportError(null);
    setExportResult(null);
    try {
      const token = await getToken();
      const res = await fetch(
        `${API_BASE_URL}/api/v1/projects/${projectId}/environments/${envId}/apps/${appName}/volume/export`,
        {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            ...(token ? { Authorization: `Bearer ${token}` } : {}),
          },
        }
      );
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        throw new Error(typeof data?.error === "string" ? data.error : t("apps.storage.export.error"));
      }
      const url = data.url as string;
      const filename = (data.filename as string) ?? "volume.tar.gz";
      setExportResult({ url, filename });
      window.location.href = url;
    } catch (e) {
      setExportError(e instanceof Error ? e.message : t("apps.storage.export.error"));
    } finally {
      setExportBusy(false);
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
        {usage &&
          (() => {
            const view = evaluateVolumeUsage(usage);
            return (
              <div className="mt-3">
                <div className="flex items-center justify-between text-xs text-gray-500 dark:text-gray-400">
                  <span>{t("apps.storage.usage.label")}</span>
                  <span className="flex items-center gap-2">
                    <span className={severityTextClass(view.bytesSeverity)}>
                      {Math.round(view.bytesRatio * 100)}%
                    </span>
                    {view.hasInodes && (
                      <span className={severityTextClass(view.inodesSeverity)}>
                        {t("apps.storage.usage.inodesShort", {
                          percent: Math.round((view.inodesRatio ?? 0) * 100),
                        })}
                      </span>
                    )}
                  </span>
                </div>
                <div className="mt-1.5 h-1.5 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-gray-800">
                  <div
                    className={severityBarClass(view.overallSeverity)}
                    style={{ width: `${Math.min(100, Math.round(view.displayRatio * 100))}%` }}
                  />
                </div>
                {view.bytesSeverity !== "ok" && (
                  <p className="mt-1.5 text-xs text-amber-600 dark:text-amber-500">{t("apps.storage.usage.warn")}</p>
                )}
                {view.hasInodes && view.inodesSeverity !== "ok" && (
                  <p className="mt-1.5 text-xs text-red-600 dark:text-red-400">
                    {t("apps.storage.usage.inodesWarn")}
                  </p>
                )}
              </div>
            );
          })()}
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
              <option key={sc.value} value={sc.value}>
                {sc.label}
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

      {current && (
        <div className="mt-6 border-t border-gray-200 dark:border-gray-800 pt-5">
          <Link
            href={`/projects/${projectId}/apps/${appName}/files?envId=${envId}`}
            className="mr-2 inline-flex items-center gap-2 rounded-lg border border-gray-300 dark:border-gray-700 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800"
          >
            {t("apps.files.open")}
          </Link>
          <button
            onClick={exportVolume}
            disabled={exportBusy}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-300 dark:border-gray-700 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50"
          >
            {exportBusy && (
              <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-gray-400 border-t-transparent" />
            )}
            {exportBusy ? t("apps.storage.export.busy") : t("apps.storage.export.button")}
          </button>

          {exportError && (
            <p className="mt-3 text-sm text-red-600 dark:text-red-400">{exportError}</p>
          )}

          {exportResult && (
            <p className="mt-3 text-sm text-green-600 dark:text-green-400">
              {t("apps.storage.export.ready")}{" "}
              <a
                href={exportResult.url}
                download={exportResult.filename}
                className="font-medium underline hover:no-underline"
              >
                {t("apps.storage.export.link")}
              </a>
            </p>
          )}

          <div className="mt-6 border-t border-gray-200 dark:border-gray-800 pt-5">
            <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">
              {t("apps.storage.report.button")}
            </h3>
            <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{t("apps.storage.report.hint")}</p>

            <button
              onClick={startReport}
              disabled={reportBusy || report?.status === "running"}
              className="mt-3 inline-flex items-center gap-2 rounded-lg border border-gray-300 dark:border-gray-700 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50"
            >
              {(reportBusy || report?.status === "running") && (
                <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-gray-400 border-t-transparent" />
              )}
              {report?.status === "running" ? t("apps.storage.report.busy") : t("apps.storage.report.button")}
            </button>

            {reportError && <p className="mt-3 text-sm text-red-600 dark:text-red-400">{reportError}</p>}

            {report?.status === "failed" && (
              <p className="mt-3 text-sm text-red-600 dark:text-red-400">
                {t("apps.storage.report.failed")}
                {report.reason ? `: ${report.reason}` : ""}
              </p>
            )}

            {report?.status === "succeeded" && (
              <div className="mt-3">
                <p className="text-sm text-gray-700 dark:text-gray-300">
                  {t("apps.storage.report.summary", {
                    bytesUsed: formatBytes(report.bytes_used ?? 0),
                    bytesTotal: formatBytes(report.bytes_total ?? 0),
                    inodesUsed: formatCount(report.inodes_used ?? 0),
                    inodesTotal: formatCount(report.inodes_total ?? 0),
                    inodesFree: formatCount(report.inodes_free ?? 0),
                  })}
                </p>

                {report.truncated && (
                  <p className="mt-1.5 text-xs text-amber-600 dark:text-amber-500">
                    {t("apps.storage.report.truncated")}
                  </p>
                )}

                <p className="mt-4 text-xs font-medium text-gray-500 dark:text-gray-400">
                  {t("apps.storage.report.topDirsTitle")}
                </p>

                {(report.top_dirs ?? []).length === 0 ? (
                  <p className="mt-2 text-sm text-gray-400 dark:text-gray-500">{t("apps.storage.report.empty")}</p>
                ) : (
                  <div className="mt-2 overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-800">
                    <table className="w-full text-left text-sm">
                      <thead className="bg-gray-50 dark:bg-gray-950 text-xs text-gray-500 dark:text-gray-400">
                        <tr>
                          <th className="px-3 py-2 font-medium">{t("apps.storage.report.colPath")}</th>
                          <th className="px-3 py-2 font-medium">{t("apps.storage.report.colFiles")}</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                        {(report.top_dirs ?? []).map((dir: VolumeMaintenanceTopDir) => (
                          <tr key={dir.path}>
                            <td className="px-3 py-2 font-mono text-xs text-gray-900 dark:text-gray-100">
                              {dir.path}
                            </td>
                            <td className="px-3 py-2 text-gray-700 dark:text-gray-300">{formatCount(dir.files)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

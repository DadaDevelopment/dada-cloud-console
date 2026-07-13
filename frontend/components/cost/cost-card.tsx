"use client";

import { useEffect, useState } from "react";
import { costApi } from "@/lib/api";
import type { CostResponse } from "@/lib/types";
import { formatRub } from "@/lib/format";
import { useT } from "@/lib/i18n/console/context";
import { Spinner } from "@/components/ui/spinner";

const WINDOWS = ["24h", "7d", "30d"] as const;

/**
 * Per-project resource cost card. Reads the backend /cost endpoint (OpenCost
 * Allocation API), shows the total for a selectable window plus a CPU/RAM/disk
 * split and a per-environment breakdown. Renders nothing when the feature is
 * unconfigured (503) so it stays invisible on stacks without OpenCost.
 */
export function CostCard({ projectId }: { projectId: string }) {
  const { t } = useT();
  const [window, setWindow] = useState<string>("30d");
  const [data, setData] = useState<CostResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [unavailable, setUnavailable] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    costApi
      .getProjectCost(projectId, window)
      .then((r) => {
        if (cancelled) return;
        setData(r);
        setUnavailable(false);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        const msg = err instanceof Error ? err.message : "";
        if (msg.includes("503") || /not configured/i.test(msg)) {
          setUnavailable(true);
        } else {
          setError(t("cost.error"));
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [projectId, window, t]);

  if (unavailable) return null;

  return (
    <div className="mb-8 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
      <div className="mb-4 flex items-center justify-between gap-3">
        <div>
          <h2 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("cost.title")}</h2>
          <p className="mt-0.5 text-xs text-gray-400 dark:text-gray-500">{t("cost.note")}</p>
        </div>
        <div className="flex shrink-0 items-center gap-1 rounded-lg bg-gray-100 dark:bg-gray-800 p-0.5">
          {WINDOWS.map((w) => (
            <button
              key={w}
              type="button"
              onClick={() => setWindow(w)}
              className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                window === w
                  ? "bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100 shadow-sm"
                  : "text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200"
              }`}
            >
              {t(`cost.window.${w}`)}
            </button>
          ))}
        </div>
      </div>

      {loading ? (
        <div className="flex h-24 items-center justify-center">
          <Spinner size="md" />
        </div>
      ) : error ? (
        <div className="text-sm text-red-600 dark:text-red-400">{error}</div>
      ) : !data || data.total <= 0 ? (
        <div className="py-6 text-center">
          <p className="text-sm font-medium text-gray-700 dark:text-gray-200">{t("cost.empty.title")}</p>
          <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("cost.empty.description")}</p>
        </div>
      ) : (
        <div>
          <div className="flex flex-wrap items-end gap-x-8 gap-y-3">
            <div>
              <div className="text-2xl font-bold text-gray-900 dark:text-gray-100">{formatRub(data.total)}</div>
              <div className="text-xs text-gray-400 dark:text-gray-500">{t("cost.total", { window: t(`cost.window.${data.window}`) })}</div>
            </div>
            <div className="flex gap-6">
              <Split label={t("cost.cpu")} value={formatRub(data.cpu)} />
              <Split label={t("cost.ram")} value={formatRub(data.ram)} />
              <Split label={t("cost.pv")} value={formatRub(data.pv)} />
            </div>
          </div>

          {data.by_environment.length > 1 && (
            <div className="mt-5 border-t border-gray-100 dark:border-gray-800 pt-4">
              <div className="mb-2 text-xs font-medium text-gray-400 dark:text-gray-500">{t("cost.byEnvironment")}</div>
              <ul className="space-y-1.5">
                {[...data.by_environment]
                  .sort((a, b) => b.total - a.total)
                  .map((e) => (
                    <li key={e.namespace} className="flex items-center justify-between text-sm">
                      <span className="font-mono text-gray-600 dark:text-gray-300">{e.environment}</span>
                      <span className="font-medium text-gray-900 dark:text-gray-100">{formatRub(e.total)}</span>
                    </li>
                  ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function Split({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-sm font-semibold text-gray-800 dark:text-gray-200">{value}</div>
      <div className="text-xs text-gray-400 dark:text-gray-500">{label}</div>
    </div>
  );
}

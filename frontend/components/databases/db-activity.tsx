"use client";

import { useCallback, useEffect, useState } from "react";
import { databasesApi } from "@/lib/api";
import type { DatabaseActivityConnection, DatabaseActivityResponse } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";

const REFRESH_MS = 10_000;

function duration(seconds: number | null): string {
  if (seconds === null || seconds < 0) return "—";
  if (seconds < 60) return `${Math.round(seconds)}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${Math.round(seconds % 60)}s`;
  return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
}

function stateTone(state: string): string {
  if (state === "active") return "bg-green-100 text-green-700 dark:bg-green-950/50 dark:text-green-400";
  if (state.startsWith("idle in transaction")) return "bg-amber-100 text-amber-700 dark:bg-amber-950/50 dark:text-amber-400";
  return "bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300";
}

function ConnectionRow({
  conn,
  canCancel,
  onCancel,
  cancelling,
}: {
  conn: DatabaseActivityConnection;
  canCancel: boolean;
  onCancel: (pid: number) => void;
  cancelling: boolean;
}) {
  const { t } = useT();
  const stuck = conn.state.startsWith("idle in transaction") && (conn.xactSeconds ?? 0) > 300;
  return (
    <div className="border-b border-gray-100 dark:border-gray-800 px-4 py-3 last:border-0">
      <div className="flex flex-wrap items-center gap-2">
        <span className={`rounded px-1.5 py-0.5 text-xs font-medium ${stateTone(conn.state)}`}>{conn.state || "—"}</span>
        <span className="font-mono text-xs text-gray-500 dark:text-gray-400">pid {conn.pid}</span>
        {conn.applicationName && <span className="text-xs text-gray-500 dark:text-gray-400">{conn.applicationName}</span>}
        {conn.waitEventType === "Lock" && (
          <span className="rounded bg-red-100 dark:bg-red-950/50 px-1.5 py-0.5 text-xs text-red-700 dark:text-red-400">
            {t("databases.activity.waitingOnLock")}
          </span>
        )}
        <span className="ml-auto text-xs text-gray-500 dark:text-gray-400">
          {t("databases.activity.inState", { time: duration(conn.stateSeconds) })}
          {conn.xactSeconds !== null && ` · ${t("databases.activity.xactOpen", { time: duration(conn.xactSeconds) })}`}
        </span>
        {canCancel && (
          <button
            onClick={() => onCancel(conn.pid)}
            disabled={cancelling}
            className="rounded-md border border-gray-200 dark:border-gray-800 px-2 py-1 text-xs font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50 transition-colors"
          >
            {cancelling ? t("databases.activity.cancelling") : t("databases.activity.cancel")}
          </button>
        )}
      </div>
      {conn.query && (
        <pre className={`mt-2 overflow-x-auto whitespace-pre-wrap break-all font-mono text-xs ${stuck ? "text-amber-700 dark:text-amber-400" : "text-gray-700 dark:text-gray-300"}`}>
          {conn.query}
        </pre>
      )}
    </div>
  );
}

/**
 * Live connection view for one managed database. Polls the tenant instance
 * through the console API so the owner can see what is running and end a
 * statement that is holding the database up.
 */
export function DbActivity({
  projectId,
  envId,
  name,
  canManage,
}: {
  projectId: string;
  envId: string;
  name: string;
  canManage: boolean;
}) {
  const { t } = useT();
  const [data, setData] = useState<DatabaseActivityResponse | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [cancelling, setCancelling] = useState<number | null>(null);

  const load = useCallback(async () => {
    if (!envId) return;
    try {
      const r = await databasesApi.activity(projectId, envId, name);
      setData(r);
      setError("");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, [projectId, envId, name]);

  useEffect(() => {
    load();
    const id = setInterval(load, REFRESH_MS);
    return () => clearInterval(id);
  }, [load]);

  const cancel = async (pid: number) => {
    setCancelling(pid);
    try {
      await databasesApi.cancelBackend(projectId, envId, name, pid);
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setCancelling(null);
    }
  };

  if (loading) {
    return (
      <section className="mt-8">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
          {t("databases.activity.title")}
        </h2>
        <div className="mt-3 flex justify-center py-6">
          <Spinner />
        </div>
      </section>
    );
  }

  return (
    <section className="mt-8">
      <div className="flex flex-wrap items-baseline gap-3">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
          {t("databases.activity.title")}
        </h2>
        {data && (
          <span className="text-xs text-gray-500 dark:text-gray-400">
            {t("databases.activity.summary", {
              total: String(data.summary.total),
              active: String(data.summary.active),
              idleInTxn: String(data.summary.idleInTransaction),
            })}
          </span>
        )}
      </div>

      {error && (
        <p className="mt-3 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-4 py-3 text-sm text-gray-600 dark:text-gray-300">
          {error}
        </p>
      )}

      {data && (
        <div className="mt-3 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 shadow-sm">
          {data.connections.length === 0 ? (
            <p className="px-4 py-6 text-sm text-gray-500 dark:text-gray-400">{t("databases.activity.empty")}</p>
          ) : (
            data.connections.map((conn) => (
              <ConnectionRow
                key={conn.pid}
                conn={conn}
                canCancel={canManage}
                cancelling={cancelling === conn.pid}
                onCancel={cancel}
              />
            ))
          )}
        </div>
      )}

      <p className="mt-2 text-xs text-gray-400 dark:text-gray-500">{t("databases.activity.hint")}</p>
    </section>
  );
}

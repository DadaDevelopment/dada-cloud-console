"use client";
import { FormEvent, useCallback, useEffect, useRef, useState } from "react";
import { logsApi, monitoringApi } from "@/lib/api";
import type { LogEntry } from "@/lib/types";
import { AnsiText } from "@/components/ansi-text";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";
import { formatLogTime } from "@/lib/log-time";

const SINCE = ["15m", "1h", "6h", "24h", "7d"] as const;
type Since = (typeof SINCE)[number];

const LIVE_POLL_MS = 5000;
const LIVE_SINCE: Since = "15m";

export function LogsViewer({
  projectId,
  vm,
  app,
  monitoring,
}: {
  projectId: string;
  vm?: string;
  app?: string;
  monitoring?: { projectId: string; envId: string; appId: string };
}) {
  const { t } = useT();
  const [query, setQuery] = useState("");
  const [since, setSince] = useState<Since>("24h");
  const [isLive, setIsLive] = useState(false);
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const liveRef = useRef(false);

  const effectiveSince = isLive ? LIVE_SINCE : since;

  const load = useCallback(
    async (q: string, silent = false) => {
      if (!silent) setLoading(true);
      try {
        let r;
        if (monitoring) {
          r = await monitoringApi.getLogs(monitoring.projectId, monitoring.envId, monitoring.appId, {
            q,
            since: effectiveSince,
            size: 300,
          });
        } else {
          r = await logsApi.search(projectId, { vm, app, q, since: effectiveSince, size: 300 });
        }
        setEntries(r.entries);
        setTotal(r.total);
        setError(null);
      } catch (e) {
        setError(e instanceof Error ? e.message : t("apps.logs.error"));
      } finally {
        if (!silent) setLoading(false);
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [projectId, vm, app, effectiveSince, monitoring?.projectId, monitoring?.envId, monitoring?.appId]
  );

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load(query);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load]);

  useEffect(() => {
    liveRef.current = isLive;
    if (!isLive) return;
    const id = setInterval(() => {
      if (liveRef.current) void load(query, true);
    }, LIVE_POLL_MS);
    return () => clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isLive, load]);

  useEffect(() => {
    if (isLive) bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [isLive, entries]);

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    void load(query);
  }

  function toggleLive() {
    setIsLive((v) => !v);
  }

  return (
    <div className="rounded-xl border border-gray-200 bg-white">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-gray-100 px-5 py-3">
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-semibold text-gray-800">{t("apps.logs.title")}</h2>
          {isLive ? (
            <span className="flex items-center gap-1 rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-700">
              <span className="relative flex h-2 w-2">
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-green-400 opacity-75" />
                <span className="relative inline-flex h-2 w-2 rounded-full bg-green-500" />
              </span>
              {t("apps.logs.live")}
            </span>
          ) : (
            <span className="text-xs text-gray-400">{t("apps.logs.matches", { total: String(total) })}</span>
          )}
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={toggleLive}
            className={`rounded-lg border px-3 py-1 text-xs font-medium transition-colors ${
              isLive
                ? "border-green-300 bg-green-50 text-green-700 hover:bg-green-100"
                : "border-gray-200 bg-white text-gray-500 hover:border-blue-300 hover:text-blue-600"
            }`}
          >
            {isLive ? t("apps.logs.stop") : t("apps.logs.live")}
          </button>
          {!isLive && (
            <div className="inline-flex rounded-lg border border-gray-200 p-0.5">
              {SINCE.map((s) => (
                <button
                  key={s}
                  onClick={() => setSince(s)}
                  className={`rounded-md px-2 py-1 text-xs font-medium transition-colors ${
                    since === s ? "bg-blue-600 text-white" : "text-gray-500 hover:bg-gray-100"
                  }`}
                >
                  {s}
                </button>
              ))}
            </div>
          )}
          {isLive && (
            <span className="text-xs text-gray-400">
              {t("apps.logs.liveStatus", { since: LIVE_SINCE, interval: String(LIVE_POLL_MS / 1000) })}
            </span>
          )}
        </div>
      </div>

      <form onSubmit={onSubmit} className="flex gap-2 px-5 py-3">
        <input
          type="text"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t("apps.logs.search.placeholder")}
          className="flex-1 rounded-lg border border-gray-300 px-3 py-1.5 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
        <button
          type="submit"
          disabled={isLive}
          className="rounded-lg bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40"
        >
          {t("apps.logs.search.button")}
        </button>
      </form>

      {error && <div className="px-5 py-3 text-sm text-red-600">{error}</div>}

      {loading ? (
        <div className="flex h-24 items-center justify-center">
          <Spinner size="sm" />
        </div>
      ) : entries.length === 0 ? (
        <div className="px-5 py-6 text-sm text-gray-400">{t("apps.logs.empty")}</div>
      ) : (
        <div className="max-h-[28rem] min-w-0 overflow-auto px-5 pb-4">
          <div className="min-w-0 rounded-lg bg-gray-900 p-3 font-mono text-xs leading-relaxed text-gray-100">
            {entries.map((e, i) => (
              <div
                key={i}
                className="flex min-w-0 max-w-full items-start gap-2 rounded px-1 py-0.5 whitespace-pre-wrap [overflow-wrap:anywhere] hover:bg-white/[0.04]"
              >
                <span className="w-[13ch] shrink-0 select-none text-gray-500">{formatLogTime(e.timestamp)}</span>
                {e.stream && (
                  <span
                    className={`shrink-0 select-none ${e.stream === "stderr" ? "text-red-400" : "text-green-400"}`}
                  >
                    {e.stream}
                  </span>
                )}
                <span className="min-w-0 max-w-full flex-1 [overflow-wrap:anywhere]">
                  <AnsiText value={e.message} />
                </span>
              </div>
            ))}
            <div ref={bottomRef} />
          </div>
        </div>
      )}
    </div>
  );
}

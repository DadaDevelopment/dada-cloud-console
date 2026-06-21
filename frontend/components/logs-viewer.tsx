"use client";
import { FormEvent, useCallback, useEffect, useState } from "react";
import { logsApi, monitoringApi } from "@/lib/api";
import type { LogEntry } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";

const SINCE = ["15m", "1h", "6h", "24h", "7d"] as const;
type Since = (typeof SINCE)[number];

// LogsViewer searches aggregated container logs (Elasticsearch/filebeat) scoped
// to a single VM or app. Distinct from the per-container Portainer tail modal:
// this searches the shipped index with free text + a time window.
// When `monitoring` prop is set, fetches via monitoringApi.getLogs instead.
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
  const [query, setQuery] = useState("");
  const [since, setSince] = useState<Since>("1h");
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(
    async (q: string) => {
      setLoading(true);
      try {
        let r;
        if (monitoring) {
          r = await monitoringApi.getLogs(monitoring.projectId, monitoring.envId, monitoring.appId, {
            q,
            since,
            size: 300,
          });
        } else {
          r = await logsApi.search(projectId, { vm, app, q, since, size: 300 });
        }
        setEntries(r.entries);
        setTotal(r.total);
        setError(null);
      } catch (e) {
        setError(e instanceof Error ? e.message : "Failed to search logs");
      } finally {
        setLoading(false);
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [projectId, vm, app, since, monitoring?.projectId, monitoring?.envId, monitoring?.appId]
  );

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load(query);
    // re-run on scope/time change only; free-text is submitted explicitly.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load]);

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    void load(query);
  }

  return (
    <div className="rounded-xl border border-gray-200 bg-white">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-gray-100 px-5 py-3">
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-semibold text-gray-800">Logs</h2>
          <span className="text-xs text-gray-400">{total} matches</span>
        </div>
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
      </div>

      <form onSubmit={onSubmit} className="flex gap-2 px-5 py-3">
        <input
          type="text"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search message text (Lucene syntax)…"
          className="flex-1 rounded-lg border border-gray-300 px-3 py-1.5 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
        <button
          type="submit"
          className="rounded-lg bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700"
        >
          Search
        </button>
      </form>

      {error && <div className="px-5 py-3 text-sm text-red-600">{error}</div>}

      {loading ? (
        <div className="flex h-24 items-center justify-center">
          <Spinner size="sm" />
        </div>
      ) : entries.length === 0 ? (
        <div className="px-5 py-6 text-sm text-gray-400">No log lines in this window.</div>
      ) : (
        <div className="max-h-[28rem] overflow-auto px-5 pb-4">
          <div className="rounded-lg bg-gray-900 p-3 font-mono text-xs leading-relaxed text-gray-100">
            {entries.map((e, i) => (
              <div key={i} className="flex gap-2 whitespace-pre-wrap break-all py-0.5">
                <span className="shrink-0 text-gray-500">{fmtTime(e.timestamp)}</span>
                {e.stream && (
                  <span
                    className={`shrink-0 ${e.stream === "stderr" ? "text-red-400" : "text-green-400"}`}
                  >
                    {e.stream}
                  </span>
                )}
                <span>{e.message}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function fmtTime(ts: string): string {
  if (!ts) return "";
  const d = new Date(ts);
  if (isNaN(d.getTime())) return ts;
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { appsApi } from "@/lib/api";
import type { AppState, PortainerContainer } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";

function shortName(c: PortainerContainer): string {
  const n = c.Names?.[0] ?? c.Id.slice(0, 12);
  return n.replace(/^\//, "");
}

function StateBadge({ state }: { state: string }) {
  const tone =
    state === "running"
      ? "bg-green-100 text-green-800"
      : state === "exited" || state === "dead"
        ? "bg-red-100 text-red-800"
        : "bg-gray-100 text-gray-700";
  return <span className={`rounded px-2 py-0.5 text-xs font-medium ${tone}`}>{state}</span>;
}

/**
 * Live compose-state panel. GetAppState proxies the docker API over an edge
 * tunnel to a remote VM, so a single 10s poll can transiently fail (returned as
 * a non-fatal `live_error` on an otherwise-200 body). To stop the error banner
 * from flickering on every blip, we keep the last-good containers and only
 * surface the Portainer error after ~3 consecutive failed polls (~30s), clearing
 * it immediately on the next success.
 */
export function ComposeStatePanel({
  projectId,
  envId,
  appName,
}: {
  projectId: string;
  envId: string;
  appName: string;
}) {
  const [state, setState] = useState<AppState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [liveError, setLiveError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const liveErrStreak = useRef(0);

  const [logsFor, setLogsFor] = useState<string | null>(null);
  const [logs, setLogs] = useState("");
  const [logsLoading, setLogsLoading] = useState(false);

  const load = useCallback(async () => {
    try {
      const s = await appsApi.getState(projectId, envId, appName);
      setError(null);
      if (s.live_error) {
        liveErrStreak.current += 1;
        setLiveError(liveErrStreak.current >= 3 ? s.live_error : null);
      } else {
        liveErrStreak.current = 0;
        setState(s);
        setLiveError(null);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load state");
    } finally {
      setLoading(false);
    }
  }, [projectId, envId, appName]);

  useEffect(() => {
    if (!envId) return undefined;
    // load() is async; setState happens after the await, not synchronously here.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load();
    const id = setInterval(() => void load(), 10000);
    return () => clearInterval(id);
  }, [load, envId]);

  const viewLogs = useCallback(
    async (containerId: string) => {
      setLogsFor(containerId);
      setLogsLoading(true);
      setLogs("");
      try {
        const r = await appsApi.getLogs(projectId, envId, appName, containerId, 300);
        setLogs(r.logs || "(no output)");
      } catch (e) {
        setLogs(e instanceof Error ? e.message : "Failed to fetch logs");
      } finally {
        setLogsLoading(false);
      }
    },
    [projectId, envId, appName]
  );

  if (loading) {
    return (
      <div className="flex h-24 items-center justify-center rounded-xl border border-gray-200 bg-white">
        <Spinner size="md" />
      </div>
    );
  }

  const containers = state?.containers ?? [];

  return (
    <div className="rounded-xl border border-gray-200 bg-white">
      <div className="flex items-center justify-between border-b border-gray-100 px-5 py-3">
        <div className="flex items-center gap-2">
          <span
            className={`inline-block h-2.5 w-2.5 rounded-full ${
              state?.online ? "bg-green-400" : "bg-gray-300"
            }`}
          />
          <h2 className="text-sm font-semibold text-gray-800">Live state</h2>
          <span className="text-xs text-gray-400">
            {state?.online ? "stack active" : "stack inactive"}
          </span>
        </div>
        <span className="text-xs text-gray-400">auto-refresh 10s</span>
      </div>

      {error && (
        <div className="px-5 py-3 text-sm text-red-600">{error}</div>
      )}
      {liveError && (
        <div className="px-5 py-2 text-xs text-amber-700">Portainer: {liveError}</div>
      )}

      {containers.length === 0 ? (
        <div className="px-5 py-6 text-sm text-gray-400">No containers reported yet.</div>
      ) : (
        <div className="divide-y divide-gray-100">
          {containers.map((c) => (
            <div key={c.Id} className="flex items-center justify-between px-5 py-3">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className="font-mono text-sm text-gray-900">{shortName(c)}</span>
                  <StateBadge state={c.State} />
                </div>
                <p className="mt-0.5 truncate text-xs text-gray-400">
                  {c.Image} · {c.Status}
                </p>
              </div>
              <button
                onClick={() => void viewLogs(c.Id)}
                className="rounded-lg border border-gray-200 px-3 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-50"
              >
                Logs
              </button>
            </div>
          ))}
        </div>
      )}

      {logsFor && (
        <div className="border-t border-gray-100 px-5 py-3">
          <div className="mb-2 flex items-center justify-between">
            <span className="text-xs font-semibold text-gray-600">
              Logs · <span className="font-mono">{logsFor.slice(0, 12)}</span>
            </span>
            <button
              onClick={() => setLogsFor(null)}
              className="text-xs text-gray-400 hover:text-gray-600"
            >
              Close
            </button>
          </div>
          {logsLoading ? (
            <div className="flex h-24 items-center justify-center">
              <Spinner size="sm" />
            </div>
          ) : (
            <pre className="max-h-80 overflow-auto rounded-lg bg-gray-900 p-3 text-xs leading-relaxed text-gray-100">
              {logs}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}

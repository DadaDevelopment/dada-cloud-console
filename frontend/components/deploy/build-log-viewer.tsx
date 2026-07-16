"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { buildsApi } from "@/lib/api";
import type { BuildLogFrame } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";

// Connection status mirrors the values-editor lifecycle so the UX matches the
// existing WS surface (values/page.tsx). The build-agent issues a short-lived
// wstoken; if the build subsystem is not configured the token endpoint 503s.
type ConnStatus = "connecting" | "open" | "closed" | "error" | "unavailable";

interface LogLine {
  id: number;
  kind: BuildLogFrame["type"];
  text: string;
}

const NOISE_PATTERNS: RegExp[] = [
  /^\s*>\s*git\b/i,
  /^Checking out Revision\b/i,
  /^Cloning the remote Git repository/i,
  /^Cloning repository/i,
  /^Fetching (without tags|upstream changes)/i,
  /^Commit message:/i,
  /^Revision\s+[0-9a-f]{7,40}\b/i,
  /\[WARNING\].*(label option is deprecated|deprecated)/i,
  /pod.?template/i,
  /^Created Pod:/i,
  /^Still waiting to schedule task/i,
  /^Agent .* is provisioned from/i,
];

function isBuildNoise(text: string): boolean {
  return NOISE_PATTERNS.some((re) => re.test(text));
}

export function BuildLogViewer({ projectId, buildId }: { projectId: string; buildId: string }) {
  const [lines, setLines] = useState<LogLine[]>([]);
  const [status, setStatus] = useState<ConnStatus>("connecting");
  const wsRef = useRef<WebSocket | null>(null);
  const counter = useRef(0);
  const bottomRef = useRef<HTMLDivElement | null>(null);

  const append = useCallback((kind: BuildLogFrame["type"], text: string) => {
    if (kind !== "error" && isBuildNoise(text)) return;
    setLines((prev) => [...prev, { id: ++counter.current, kind, text }]);
  }, []);

  const connect = useCallback(async () => {
    setStatus("connecting");

    let tokenData: { token: string; ws_url: string };
    try {
      tokenData = await buildsApi.logsToken(projectId, buildId);
    } catch (e) {
      // 503 → build subsystem not wired yet; surface a calm disabled state
      // rather than an error toast loop.
      setStatus("unavailable");
      append("error", e instanceof Error ? e.message : "Build logs are not available yet");
      return;
    }

    const url = new URL("/ws/build", tokenData.ws_url.replace(/^http/, "ws"));
    url.searchParams.set("token", tokenData.token);

    const ws = new WebSocket(url.toString());
    wsRef.current = ws;

    ws.onopen = () => setStatus("open");
    ws.onclose = () => setStatus("closed");
    ws.onerror = () => setStatus("error");

    ws.onmessage = (ev) => {
      try {
        const frame = JSON.parse(ev.data) as BuildLogFrame;
        append(frame.type, frame.line ?? frame.msg ?? "");
      } catch {
        // ignore malformed frames
      }
    };
  }, [projectId, buildId, append]);

  useEffect(() => {
    void connect(); // eslint-disable-line react-hooks/set-state-in-effect
    return () => {
      wsRef.current?.close();
    };
  }, [connect]);

  // Keep the view pinned to the latest log line.
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [lines]);

  if (status === "unavailable") {
    return (
      <div className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
        Live build logs are not available yet — the build subsystem is not configured in this environment.
      </div>
    );
  }

  return (
    <div className="overflow-hidden rounded-lg border border-gray-800 bg-gray-950">
      <div className="flex items-center justify-between border-b border-gray-800 px-4 py-2">
        <span className="text-xs font-medium text-gray-400">Build logs</span>
        <StatusDot status={status} onReconnect={connect} />
      </div>
      <div className="max-h-[520px] overflow-y-auto p-4 font-mono text-xs leading-relaxed">
        {lines.length === 0 && status === "connecting" ? (
          <div className="flex items-center gap-2 text-gray-500">
            <Spinner size="sm" /> Connecting to build stream…
          </div>
        ) : lines.length === 0 ? (
          <p className="text-gray-600">No log output yet.</p>
        ) : (
          lines.map((l) => (
            <div
              key={l.id}
              className={
                l.kind === "error"
                  ? "text-red-400"
                  : l.kind === "status"
                    ? "text-blue-300"
                    : "text-gray-300"
              }
            >
              {l.kind === "status" ? `▸ ${l.text}` : l.text}
            </div>
          ))
        )}
        <div ref={bottomRef} />
      </div>
    </div>
  );
}

function StatusDot({ status, onReconnect }: { status: ConnStatus; onReconnect: () => void }) {
  const colors: Record<ConnStatus, string> = {
    connecting: "bg-yellow-400",
    open: "bg-green-400",
    closed: "bg-gray-400",
    error: "bg-red-400",
    unavailable: "bg-gray-400",
  };
  const labels: Record<ConnStatus, string> = {
    connecting: "Connecting…",
    open: "Streaming",
    closed: "Disconnected",
    error: "Error",
    unavailable: "Unavailable",
  };
  return (
    <span className="inline-flex items-center gap-2 text-xs text-gray-400">
      <span className={`inline-block h-2 w-2 rounded-full ${colors[status]}`} />
      {labels[status]}
      {(status === "closed" || status === "error") && (
        <button onClick={onReconnect} className="ml-1 text-blue-400 hover:text-blue-300">
          Reconnect
        </button>
      )}
    </span>
  );
}

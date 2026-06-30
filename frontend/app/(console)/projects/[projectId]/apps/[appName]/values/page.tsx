"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { useParams, useSearchParams } from "next/navigation";
import { valuesApi } from "@/lib/api";
import { YamlEditor } from "@/components/ui/yaml-editor";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canEditYaml } from "@/lib/rbac";
import { useT } from "@/lib/i18n/console/context";

// ─── WebSocket message shapes ────────────────────────────────────────────────

type WsIncoming =
  | { type: "content"; yaml: string }
  | { type: "update"; yaml: string }
  | { type: "committed"; sha: string }
  | { type: "error"; message: string };

type WsOutgoing =
  | { type: "save"; yaml: string };

// ─── Connection status indicator ─────────────────────────────────────────────

type ConnStatus = "connecting" | "open" | "closed" | "error";

function StatusDot({ status }: { status: ConnStatus }) {
  const { t } = useT();
  const colors: Record<ConnStatus, string> = {
    connecting: "bg-yellow-400",
    open:       "bg-green-400",
    closed:     "bg-gray-400",
    error:      "bg-red-400",
  };
  const labels: Record<ConnStatus, string> = {
    connecting: t("apps.values.status.connecting"),
    open:       t("apps.values.status.open"),
    closed:     t("apps.values.status.closed"),
    error:      t("apps.values.status.error"),
  };
  return (
    <span className="inline-flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400">
      <span className={`inline-block h-2 w-2 rounded-full ${colors[status]}`} />
      {labels[status]}
    </span>
  );
}

// ─── Toast ────────────────────────────────────────────────────────────────────

interface Toast {
  id: number;
  kind: "success" | "error" | "info";
  text: string;
}

function useToasts() {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const counter = useRef(0);

  const push = useCallback((kind: Toast["kind"], text: string) => {
    const id = ++counter.current;
    setToasts((prev) => [...prev, { id, kind, text }]);
    setTimeout(() => setToasts((prev) => prev.filter((t) => t.id !== id)), 4000);
  }, []);

  return { toasts, push };
}

// ─── Page ─────────────────────────────────────────────────────────────────────

export default function ValuesPage() {
  const params = useParams<{ projectId: string; appName: string }>();
  const searchParams = useSearchParams();
  const { role, loading: roleLoading, selectedEnv } = useProjectContext();
  const allowed = canEditYaml(role);
  const { projectId, appName } = params;
  const envId = searchParams.get("envId") || selectedEnv?.id || "";
  const { t } = useT();

  const [yaml, setYaml] = useState("");
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [status, setStatus] = useState<ConnStatus>("connecting");
  const { toasts, push } = useToasts();

  const wsRef = useRef<WebSocket | null>(null);
  const yamlRef = useRef(yaml); // stable ref for the send handler

  useEffect(() => { yamlRef.current = yaml; }, [yaml]);

  // ── Connect ─────────────────────────────────────────────────────────────────
  const connect = useCallback(async () => {
    if (!envId) return;
    setStatus("connecting");

    let tokenData: { token: string; ws_url: string };
    try {
      tokenData = await valuesApi.getToken(projectId, envId, appName);
    } catch (e) {
      setStatus("error");
      push("error", e instanceof Error ? e.message : t("apps.values.error.token"));
      return;
    }

    // Resolve project/env slugs from the app path returned by the agent.
    // We pass them as query params so the agent can verify the token claims.
    // The backend already embedded the slugs in the token; we also need to
    // send them as plain params so the agent can cross-check.
    // We don't have slugs here — but the token itself already encodes them.
    // The agent will decode the token and use those slugs, so we just need
    // to forward the same values. We ask the backend to return them too.
    //
    // ⚠ NOTE: ws_url comes from GITOPS_AGENT_WS_URL, e.g. "wss://gitops.example.com"
    //   The agent parses project/env/app from the token, not from query params —
    //   but also validates that query params MATCH the token claims.
    //   We forward query params for clarity; the token is authoritative.
    const url = new URL("/ws/values", tokenData.ws_url.replace(/^http/, "ws"));
    url.searchParams.set("token", tokenData.token);

    const ws = new WebSocket(url.toString());
    wsRef.current = ws;

    ws.onopen = () => setStatus("open");
    ws.onclose = () => setStatus("closed");
    ws.onerror = () => { setStatus("error"); push("error", t("apps.values.error.ws")); };

    ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data) as WsIncoming;
        if (msg.type === "content") {
          setYaml(msg.yaml);
          setDirty(false);
        } else if (msg.type === "update") {
          // Remote change arrived; if the user has unsaved edits, warn them.
          if (dirty) {
            push("info", t("apps.values.toast.updated"));
          } else {
            setYaml(msg.yaml);
          }
        } else if (msg.type === "committed") {
          push("success", t("apps.values.toast.committed", { sha: msg.sha.slice(0, 7) }));
          setSaving(false);
          setDirty(false);
        } else if (msg.type === "error") {
          push("error", msg.message ?? t("apps.values.error.unknown"));
          setSaving(false);
        }
      } catch {
        // ignore malformed frames
      }
    };

    return () => {
      ws.close();
    };
  }, [projectId, envId, appName]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const cleanup = connect(); // eslint-disable-line react-hooks/set-state-in-effect
    return () => {
      cleanup?.then((fn) => fn?.());
      wsRef.current?.close();
    };
  }, [connect]);

  // ── Cmd/Ctrl+S ──────────────────────────────────────────────────────────────
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "s") {
        e.preventDefault();
        handleSave();
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }); // no deps — always uses latest handleSave via closure

  // ── Save ─────────────────────────────────────────────────────────────────────
  function handleSave() {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN || saving || !dirty) return;
    setSaving(true);
    const msg: WsOutgoing = { type: "save", yaml: yamlRef.current };
    ws.send(JSON.stringify(msg));
  }

  // ── Render ───────────────────────────────────────────────────────────────────
  if (!roleLoading && !allowed) {
    return (
      <div>
        <Breadcrumb
          items={[
            { label: t("common.crumb.projects"), href: "/projects" },
            { label: t("nav.apps"), href: `/projects/${projectId}/apps` },
            { label: appName, href: `/projects/${projectId}/apps/${appName}${envId ? `?envId=${envId}` : ""}` },
            { label: t("apps.values.crumb") },
          ]}
        />
        <div className="mt-4 rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-800 dark:text-amber-300">
          {t("apps.values.error.noPermission")}
        </div>
      </div>
    );
  }

  return (
    <div>
      {/* Header */}
      <div className="mb-6 flex items-start justify-between">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.projects"), href: "/projects" },
              { label: t("common.crumb.overview"), href: `/projects/${projectId}` },
              { label: t("nav.apps"), href: `/projects/${projectId}/apps` },
              { label: appName, href: `/projects/${projectId}/apps/${appName}${envId ? `?envId=${envId}` : ""}` },
              { label: t("apps.values.crumb") },
            ]}
          />
          <div className="mt-2 flex items-center gap-3">
            <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">
              <span className="font-mono">{appName}</span>
              <span className="ml-2 text-gray-400 dark:text-gray-500 font-normal text-lg">{t("apps.values.heading.suffix")}</span>
            </h1>
            <StatusDot status={status} />
          </div>
        </div>

        <div className="flex items-center gap-3">
          {status === "closed" || status === "error" ? (
            <button
              onClick={connect}
              className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-50 transition-colors"
            >
              {t("apps.values.reconnect")}
            </button>
          ) : null}
          <button
            onClick={handleSave}
            disabled={!dirty || saving || status !== "open"}
            className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40 transition-colors"
          >
            {saving ? <><Spinner size="sm" />{t("apps.values.saving")}</> : <>{t("apps.values.save")}</>}
          </button>
        </div>
      </div>

      {/* Dirty indicator */}
      {dirty && !saving && (
        <p className="mb-3 text-xs text-yellow-600 dark:text-yellow-400">
          {t("apps.values.unsaved")} <kbd className="rounded bg-yellow-100 px-1 py-0.5 font-mono text-yellow-700 dark:text-yellow-300">Cmd+S</kbd> to save
        </p>
      )}

      {/* Editor */}
      {status === "connecting" && !yaml ? (
        <div className="flex h-64 items-center justify-center rounded-lg border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900">
          <Spinner size="lg" />
        </div>
      ) : (
        <YamlEditor
          value={yaml}
          onChange={(v) => { setYaml(v); setDirty(true); }}
          minHeight="600px"
        />
      )}

      {/* Toasts */}
      <div role="status" aria-live="polite" className="fixed bottom-6 right-6 z-50 flex flex-col gap-2">
        {toasts.map((t) => (
          <div
            key={t.id}
            className={`rounded-lg px-4 py-3 text-sm font-medium shadow-lg transition-all ${
              t.kind === "success" ? "bg-green-600 text-white" :
              t.kind === "error"   ? "bg-red-600 text-white" :
                                     "bg-gray-800 text-white"
            }`}
          >
            {t.text}
          </div>
        ))}
      </div>
    </div>
  );
}

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

// ─── WebSocket message shapes (shared with the values editor) ────────────────
type WsIncoming =
  | { type: "content"; yaml: string }
  | { type: "update"; yaml: string }
  | { type: "committed"; sha: string }
  | { type: "error"; message: string };

type ConnStatus = "connecting" | "open" | "closed" | "error";

function StatusDot({ status }: { status: ConnStatus }) {
  const colors: Record<ConnStatus, string> = {
    connecting: "bg-yellow-400",
    open: "bg-green-400",
    closed: "bg-gray-400",
    error: "bg-red-400",
  };
  return <span className={`inline-block h-2 w-2 rounded-full ${colors[status]}`} />;
}

// ─── One editable file (compose.yaml or .env) over its own WS ────────────────
function ComposeFilePane({
  projectId,
  envId,
  appName,
  file,
  onToast,
}: {
  projectId: string;
  envId: string;
  appName: string;
  file: "compose.yaml" | ".env";
  onToast: (kind: "success" | "error" | "info", text: string) => void;
}) {
  const { t } = useT();
  const [content, setContent] = useState("");
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [status, setStatus] = useState<ConnStatus>("connecting");
  const wsRef = useRef<WebSocket | null>(null);
  const contentRef = useRef(content);
  useEffect(() => {
    contentRef.current = content;
  }, [content]);

  const connect = useCallback(async () => {
    if (!envId) return undefined;
    let tokenData: { token: string; ws_url: string };
    try {
      tokenData = await valuesApi.getToken(projectId, envId, appName, file);
    } catch (e) {
      setStatus("error");
      onToast("error", e instanceof Error ? e.message : t("apps.values.error.token"));
      return undefined;
    }

    const url = new URL("/ws/file", tokenData.ws_url.replace(/^http/, "ws"));
    url.searchParams.set("token", tokenData.token);
    const ws = new WebSocket(url.toString());
    wsRef.current = ws;

    ws.onopen = () => setStatus("open");
    ws.onclose = () => setStatus("closed");
    ws.onerror = () => {
      setStatus("error");
      onToast("error", `${file}: ${t("apps.values.error.ws")}`);
    };
    ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data) as WsIncoming;
        if (msg.type === "content") {
          setContent(msg.yaml);
          setDirty(false);
        } else if (msg.type === "update") {
          if (!dirty) setContent(msg.yaml);
          else onToast("info", `${file} ${t("apps.values.toast.updated")}`);
        } else if (msg.type === "committed") {
          onToast("success", `${file} ${t("apps.values.toast.committed", { sha: msg.sha.slice(0, 7) })}`);
          setSaving(false);
          setDirty(false);
        } else if (msg.type === "error") {
          onToast("error", `${file}: ${msg.message ?? t("apps.values.error.unknown")}`);
          setSaving(false);
        }
      } catch {
        /* ignore malformed frames */
      }
    };
    return () => ws.close();
  }, [projectId, envId, appName, file]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    let cleanup: (() => void) | undefined;
    // connect() is async; all setState calls happen after an await or inside
    // WebSocket event callbacks (external system), not synchronously here.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void connect().then((fn) => {
      cleanup = fn;
    });
    return () => {
      cleanup?.();
      wsRef.current?.close();
    };
  }, [connect]);

  const save = useCallback(() => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN || saving || !dirty) return;
    setSaving(true);
    ws.send(JSON.stringify({ type: "save", yaml: contentRef.current }));
  }, [saving, dirty]);

  return (
    <div className="flex flex-col rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900">
      <div className="flex items-center justify-between border-b border-gray-100 dark:border-gray-800 px-4 py-2.5">
        <div className="flex items-center gap-2">
          <StatusDot status={status} />
          <span className="font-mono text-sm font-semibold text-gray-800 dark:text-gray-200">{file}</span>
          {dirty && !saving && <span className="text-xs text-yellow-600 dark:text-yellow-400">{t("apps.compose.pane.unsaved")}</span>}
        </div>
        <button
          onClick={save}
          disabled={!dirty || saving || status !== "open"}
          className="inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700 disabled:opacity-40"
        >
          {saving ? (
            <>
              <Spinner size="sm" />
              {t("apps.compose.pane.saving")}
            </>
          ) : (
            t("apps.compose.pane.save")
          )}
        </button>
      </div>
      {status === "connecting" && !content ? (
        <div className="flex h-64 items-center justify-center">
          <Spinner size="lg" />
        </div>
      ) : (
        <YamlEditor
          value={content}
          onChange={(v) => {
            setContent(v);
            setDirty(true);
          }}
          minHeight="520px"
        />
      )}
    </div>
  );
}

interface Toast {
  id: number;
  kind: "success" | "error" | "info";
  text: string;
}

export default function ComposePage() {
  const params = useParams<{ projectId: string; appName: string }>();
  const searchParams = useSearchParams();
  const { projectId, appName } = params;
  const { role, loading: roleLoading, selectedEnv } = useProjectContext();
  const envId = searchParams.get("envId") || selectedEnv?.id || "";
  const allowed = canEditYaml(role);
  const { t } = useT();

  const [toasts, setToasts] = useState<Toast[]>([]);
  const counter = useRef(0);
  const pushToast = useCallback((kind: Toast["kind"], text: string) => {
    const id = ++counter.current;
    setToasts((prev) => [...prev, { id, kind, text }]);
    setTimeout(() => setToasts((prev) => prev.filter((t) => t.id !== id)), 4000);
  }, []);

  return (
    <div>
      <div className="mb-6">
        <Breadcrumb
          items={[
            { label: t("common.crumb.projects"), href: "/projects" },
            { label: t("nav.apps"), href: `/projects/${projectId}/apps` },
            { label: appName, href: `/projects/${projectId}/apps/${appName}${envId ? `?envId=${envId}` : ""}` },
            { label: t("apps.compose.crumb") },
          ]}
        />
        <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">
          <span className="font-mono">{appName}</span>
          <span className="ml-2 text-lg font-normal text-gray-400 dark:text-gray-500">{t("apps.compose.heading.suffix")}</span>
        </h1>
        <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">
          {t("apps.compose.subtitle")}
        </p>
      </div>

      {!roleLoading && !allowed ? (
        <div className="rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-800 dark:text-amber-300">
          {t("apps.compose.error.noPermission")}
        </div>
      ) : !envId ? (
        <div className="rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-700 dark:text-amber-300">
          {t("apps.compose.error.noEnv")}
        </div>
      ) : (
        <div className="grid gap-5 lg:grid-cols-2">
          <ComposeFilePane
            projectId={projectId}
            envId={envId}
            appName={appName}
            file="compose.yaml"
            onToast={pushToast}
          />
          <ComposeFilePane
            projectId={projectId}
            envId={envId}
            appName={appName}
            file=".env"
            onToast={pushToast}
          />
        </div>
      )}

      <div role="status" aria-live="polite" className="fixed bottom-6 right-6 z-50 flex flex-col gap-2">
        {toasts.map((t) => (
          <div
            key={t.id}
            className={`rounded-lg px-4 py-3 text-sm font-medium shadow-lg ${
              t.kind === "success"
                ? "bg-green-600 text-white"
                : t.kind === "error"
                  ? "bg-red-600 text-white"
                  : "bg-gray-800 text-white"
            }`}
          >
            {t.text}
          </div>
        ))}
      </div>
    </div>
  );
}

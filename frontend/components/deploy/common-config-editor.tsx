"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { valuesApi } from "@/lib/api";
import { readCommon, patchCommon, type CommonConfig } from "@/lib/values-common";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";
import { useClaims } from "@/lib/claims";

type WsIncoming =
  | { type: "content"; yaml: string }
  | { type: "update"; yaml: string }
  | { type: "committed"; sha: string }
  | { type: "error"; message: string };

type ConnStatus = "connecting" | "open" | "closed" | "error";

interface Props {
  projectId: string;
  envId: string;
  appName: string;
  canEdit: boolean;
}

const EMPTY: CommonConfig = {
  imageName: "",
  imageTag: "",
  servicePort: "",
  replicas: "",
  useDotEnv: "false",
  reqCpu: "",
  reqMemory: "",
  limCpu: "",
  limMemory: "",
};

/**
 * CommonConfigEditor is a structured form over an app's Helm values.yaml
 * common.* block. It reuses the same delegated WebSocket the raw YAML editor
 * uses (valuesApi.getToken -> /ws/values), reads the current common.* fields,
 * and on save merges the form back into the untouched YAML and commits it. The
 * raw YAML editor stays available for anything this form does not expose.
 */
export function CommonConfigEditor({ projectId, envId, appName, canEdit }: Props) {
  const { t } = useT();
  const godMode = !!useClaims()?.platformAdmin;
  const minReplicas = godMode ? 0 : 1;
  const [cfg, setCfg] = useState<CommonConfig>(EMPTY);
  const [loaded, setLoaded] = useState(false);
  const [status, setStatus] = useState<ConnStatus>("connecting");
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  const wsRef = useRef<WebSocket | null>(null);
  const yamlRef = useRef<string>("");

  const connect = useCallback(async () => {
    if (!envId) return;
    setStatus("connecting");
    let tok: { token: string; ws_url: string };
    try {
      tok = await valuesApi.getToken(projectId, envId, appName);
    } catch (e) {
      setStatus("error");
      setMsg({ kind: "err", text: e instanceof Error ? e.message : t("apps.config.error.token") });
      return;
    }
    const url = new URL("/ws/values", tok.ws_url.replace(/^http/, "ws"));
    url.searchParams.set("token", tok.token);
    const ws = new WebSocket(url.toString());
    wsRef.current = ws;
    ws.onopen = () => setStatus("open");
    ws.onclose = () => setStatus("closed");
    ws.onerror = () => setStatus("error");
    ws.onmessage = (ev) => {
      try {
        const m = JSON.parse(ev.data) as WsIncoming;
        if (m.type === "content") {
          yamlRef.current = m.yaml;
          setCfg(readCommon(m.yaml));
          setLoaded(true);
        } else if (m.type === "update") {
          yamlRef.current = m.yaml;
        } else if (m.type === "committed") {
          setSaving(false);
          setMsg({ kind: "ok", text: t("apps.config.saved", { sha: m.sha.slice(0, 7) }) });
        } else if (m.type === "error") {
          setSaving(false);
          setMsg({ kind: "err", text: m.message || t("apps.config.error.unknown") });
        }
      } catch {
        setMsg((prev) => prev);
      }
    };
  }, [projectId, envId, appName, t]);

  useEffect(() => {
    const timer = setTimeout(connect, 0);
    return () => {
      clearTimeout(timer);
      wsRef.current?.close();
    };
  }, [connect]);

  function set<K extends keyof CommonConfig>(k: K, v: string) {
    setCfg((c) => ({ ...c, [k]: v }));
  }

  function validate(): string | null {
    const port = Number(cfg.servicePort);
    if (!Number.isInteger(port) || port < 1 || port > 65535) return t("apps.config.invalid.port");
    const rep = Number(cfg.replicas);
    if (!Number.isInteger(rep) || rep < minReplicas || rep > 10) return t("apps.config.invalid.replicas", { min: String(minReplicas) });
    if (!cfg.imageName.trim() || !cfg.imageTag.trim()) return t("apps.config.invalid.image");
    if (!cfg.reqCpu.trim() || !cfg.reqMemory.trim() || !cfg.limCpu.trim() || !cfg.limMemory.trim()) {
      return t("apps.config.invalid.resources");
    }
    return null;
  }

  function save() {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    const err = validate();
    if (err) {
      setMsg({ kind: "err", text: err });
      return;
    }
    setSaving(true);
    setMsg(null);
    const merged = patchCommon(yamlRef.current, cfg);
    yamlRef.current = merged;
    ws.send(JSON.stringify({ type: "save", yaml: merged }));
  }

  const field =
    "mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 disabled:opacity-50";
  const label = "block text-sm font-medium text-gray-700 dark:text-gray-300";
  const disabled = !canEdit || saving || status !== "open";
  const dotColor: Record<ConnStatus, string> = {
    connecting: "bg-yellow-400",
    open: "bg-green-400",
    closed: "bg-gray-400",
    error: "bg-red-400",
  };
  const statusLabel: Record<ConnStatus, string> = {
    connecting: t("apps.values.status.connecting"),
    open: t("apps.values.status.open"),
    closed: t("apps.values.status.closed"),
    error: t("apps.values.status.error"),
  };

  return (
    <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-6">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("apps.config.title")}</h2>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("apps.config.subtitle")}</p>
        </div>
        <span className="inline-flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400">
          <span className={`inline-block h-2 w-2 rounded-full ${dotColor[status]}`} />
          {statusLabel[status]}
        </span>
      </div>

      {!loaded ? (
        <div className="mt-6 flex h-40 items-center justify-center">
          {status === "error" || status === "closed" ? (
            <button
              onClick={connect}
              className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-50"
            >
              {t("apps.values.reconnect")}
            </button>
          ) : (
            <Spinner size="lg" />
          )}
        </div>
      ) : (
        <>
          <div className="mt-5 grid gap-4 sm:grid-cols-2">
            <div className="sm:col-span-2">
              <label className={label}>{t("apps.config.imageName")}</label>
              <input className={field} value={cfg.imageName} onChange={(e) => set("imageName", e.target.value)} disabled={disabled} />
            </div>
            <div className="sm:col-span-2">
              <label className={label}>{t("apps.config.imageTag")}</label>
              <input className={field} value={cfg.imageTag} onChange={(e) => set("imageTag", e.target.value)} disabled={disabled} />
              <p className="mt-1 text-xs text-gray-400">{t("apps.config.imageHint")}</p>
            </div>
            <div>
              <label className={label}>{t("apps.config.servicePort")}</label>
              <input className={field} type="number" min={1} max={65535} value={cfg.servicePort} onChange={(e) => set("servicePort", e.target.value)} disabled={disabled} />
            </div>
            <div>
              <label className={label}>{t("apps.config.replicas")}</label>
              <input className={field} type="number" min={minReplicas} max={10} value={cfg.replicas} onChange={(e) => set("replicas", e.target.value)} disabled={disabled} />
            </div>
            <div className="sm:col-span-2">
              <label className={label}>{t("apps.config.useDotEnv")}</label>
              <select className={field} value={cfg.useDotEnv} onChange={(e) => set("useDotEnv", e.target.value)} disabled={disabled}>
                <option value="false">false</option>
                <option value="true">true</option>
              </select>
              <p className="mt-1 text-xs text-gray-400">{t("apps.config.useDotEnvHint")}</p>
            </div>
            <div>
              <label className={label}>{t("apps.config.reqCpu")}</label>
              <input className={field} value={cfg.reqCpu} onChange={(e) => set("reqCpu", e.target.value)} disabled={disabled} placeholder="10m" />
            </div>
            <div>
              <label className={label}>{t("apps.config.reqMemory")}</label>
              <input className={field} value={cfg.reqMemory} onChange={(e) => set("reqMemory", e.target.value)} disabled={disabled} placeholder="128Mi" />
            </div>
            <div>
              <label className={label}>{t("apps.config.limCpu")}</label>
              <input className={field} value={cfg.limCpu} onChange={(e) => set("limCpu", e.target.value)} disabled={disabled} placeholder="250m" />
            </div>
            <div>
              <label className={label}>{t("apps.config.limMemory")}</label>
              <input className={field} value={cfg.limMemory} onChange={(e) => set("limMemory", e.target.value)} disabled={disabled} placeholder="256Mi" />
            </div>
          </div>

          {msg && (
            <p className={`mt-4 text-sm ${msg.kind === "ok" ? "text-green-600 dark:text-green-400" : "text-red-600 dark:text-red-400"}`}>
              {msg.text}
            </p>
          )}

          <button
            onClick={save}
            disabled={disabled}
            className="mt-4 inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {saving ? (
              <>
                <Spinner size="sm" />
                {t("apps.config.saving")}
              </>
            ) : (
              t("apps.config.save")
            )}
          </button>
        </>
      )}
    </div>
  );
}

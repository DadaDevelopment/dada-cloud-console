"use client";

import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { usePathname, useRouter } from "next/navigation";
import { X, Send, Bot, Loader2, Wrench, AlertTriangle, Check, Ban, RotateCcw } from "lucide-react";
import { useT } from "@/lib/i18n/console/context";
import { useProjectContext } from "@/lib/project-context";
import { getToken } from "@/lib/api";
import { renderMarkdown } from "@/lib/markdown";
import { autolinkConsolePaths, isInternalConsolePath, isKnownConsoleRoute } from "@/lib/agent-chat-links";
import { confirmArgEntries } from "@/lib/agent-chat-redact";

type ChatMessage =
  | { id: string; kind: "message"; role: "user" | "assistant"; content: string; pending?: boolean }
  | { id: string; kind: "tool_call"; name: string }
  | { id: string; kind: "error"; code: string; message: string }
  | {
      id: string;
      kind: "confirm";
      actionId: string;
      toolName: string;
      args: Record<string, unknown>;
      summary?: string;
      priceRub?: number | null;
      acknowledged?: boolean;
      resolved?: "approved" | "rejected" | "error";
    };

/**
 * How much the agent may do without stopping to ask. "edit" is the default:
 * reversible operational actions run on their own, anything that destroys
 * data, spends money or exposes something publicly still opens a card. The
 * backend re-derives the same rule from the mode it is sent, so a tampered
 * value can only ever cost the user extra confirmations, never fewer than the
 * backend's own classification allows.
 */
type AgentMode = "manual" | "edit" | "admin";

const AGENT_MODES: AgentMode[] = ["manual", "edit", "admin"];

const AGENT_MODE_STORAGE_KEY = "dada.agentChat.mode";

const modeListeners = new Set<() => void>();

/**
 * The mode lives in localStorage, which is an external store: it survives
 * reloads, it is shared by every panel on the page and another tab can change
 * it. Reading it through useSyncExternalStore keeps the server render at the
 * default instead of guessing, so there is no hydration mismatch and no
 * setState in an effect.
 */
function subscribeMode(onChange: () => void): () => void {
  modeListeners.add(onChange);
  window.addEventListener("storage", onChange);
  return () => {
    modeListeners.delete(onChange);
    window.removeEventListener("storage", onChange);
  };
}

function readStoredMode(): AgentMode {
  const stored = window.localStorage.getItem(AGENT_MODE_STORAGE_KEY);
  return AGENT_MODES.includes(stored as AgentMode) ? (stored as AgentMode) : "edit";
}

function writeStoredMode(next: AgentMode) {
  window.localStorage.setItem(AGENT_MODE_STORAGE_KEY, next);
  for (const listener of modeListeners) listener();
}

const AGENT_ERROR_CODE_KEYS: Record<string, string> = {
  not_configured: "agentChat.error.notConfigured",
  daily_cap: "agentChat.error.dailyCap",
  upstream: "agentChat.error.upstream",
};

const TOOL_NAME_KEYS: Record<string, string> = {
  restartApp: "agentChat.tool.restartApp",
  triggerBuild: "agentChat.tool.triggerBuild",
  deployTrigger: "agentChat.tool.deployTrigger",
  cancelBuild: "agentChat.tool.cancelBuild",
  retryOperation: "agentChat.tool.retryOperation",
  setEnvVar: "agentChat.tool.setEnvVar",
  deleteEnvVar: "agentChat.tool.deleteEnvVar",
  rollbackApp: "agentChat.tool.rollbackApp",
  rollbackDeployment: "agentChat.tool.rollbackDeployment",
  promoteDeployment: "agentChat.tool.promoteDeployment",
  updateAppImage: "agentChat.tool.updateAppImage",
  updateAppProfile: "agentChat.tool.updateAppProfile",
  updateAppStorage: "agentChat.tool.updateAppStorage",
  createDatabase: "agentChat.tool.createDatabase",
  createEndpoint: "agentChat.tool.createEndpoint",
  createS3Bucket: "agentChat.tool.createS3Bucket",
};

function appNameFromPath(pathname: string): string | undefined {
  const segs = pathname.split("/").filter(Boolean);
  const idx = segs.indexOf("apps");
  return idx >= 0 && segs[idx + 1] ? decodeURIComponent(segs[idx + 1]) : undefined;
}

function newId(): string {
  return `${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;
}

interface ConfirmRequestPayload {
  actionId: string;
  toolName: string;
  args: Record<string, unknown>;
  summary?: string;
  priceRub?: number | null;
}

interface StreamChatHandlers {
  onToken: (chunk: string) => void;
  onToolCall: (name: string) => void;
  onConfirmRequest: (req: ConfirmRequestPayload) => void;
  onNavigate: (path: string) => void;
  onError: (code: string, message: string) => void;
  onDone?: (awaitingConfirm: boolean) => void;
}

/**
 * Reads the path out of a navigate event, or null when the assistant asked for
 * somewhere this console does not render.
 *
 * The backend checks the path against its own copy of the route table before
 * sending, so this is the second half of the same gate: the panel moves the
 * user's page without them clicking anything, and the one thing worse than a
 * dead link is a page that navigates itself to a 404.
 */
function parseNavigateData(data: string): string | null {
  try {
    const parsed = JSON.parse(data) as { path?: string };
    const path = parsed.path ?? "";
    if (!isInternalConsolePath(path) || !isKnownConsoleRoute(path)) return null;
    return path;
  } catch {
    return null;
  }
}

function parseToolCallData(data: string): string | null {
  try {
    const parsed = JSON.parse(data) as { name?: string };
    return parsed.name ?? null;
  } catch {
    return null;
  }
}

function parseErrorData(data: string): { code: string; message: string } {
  try {
    const parsed = JSON.parse(data) as { code?: string; message?: string };
    return { code: parsed.code ?? "upstream", message: parsed.message ?? "" };
  } catch {
    return { code: "upstream", message: "" };
  }
}

function parseConfirmRequestData(data: string): ConfirmRequestPayload | null {
  try {
    const parsed = JSON.parse(data) as {
      action_id?: string;
      tool_name?: string;
      args?: Record<string, unknown>;
      summary?: string;
      price_rub?: number | null;
    };
    if (!parsed.action_id || !parsed.tool_name) return null;
    return {
      actionId: parsed.action_id,
      toolName: parsed.tool_name,
      args: parsed.args ?? {},
      summary: parsed.summary,
      priceRub: parsed.price_rub ?? null,
    };
  } catch {
    return null;
  }
}

// formatRub renders a monthly RUB estimate for the confirm card, e.g. "≈450 ₽/мес".
function formatRub(rub: number): string {
  return `≈${Math.round(rub).toLocaleString("ru-RU")} ₽/мес`;
}

function parseDoneData(data: string): boolean {
  try {
    const parsed = JSON.parse(data) as { awaiting_confirm?: boolean };
    return Boolean(parsed.awaiting_confirm);
  } catch {
    return false;
  }
}

async function streamSSE(url: string, body: unknown, handlers: StreamChatHandlers): Promise<void> {
  const token = await getToken();
  const res = await fetch(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: JSON.stringify(body),
  });

  if (!res.ok || !res.body) {
    throw new Error(`agent chat request failed (${res.status})`);
  }

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let currentEvent = "message";
  let dataLines: string[] = [];

  const dispatch = (): boolean => {
    const event = currentEvent;
    const data = dataLines.join("\n");
    currentEvent = "message";
    dataLines = [];
    if (data === "" && event === "message") return false;
    switch (event) {
      case "token":
        handlers.onToken(data);
        return false;
      case "tool_call": {
        const name = parseToolCallData(data);
        if (name) handlers.onToolCall(name);
        return false;
      }
      case "confirm_request": {
        const req = parseConfirmRequestData(data);
        if (req) handlers.onConfirmRequest(req);
        return false;
      }
      case "navigate": {
        const path = parseNavigateData(data);
        if (path) handlers.onNavigate(path);
        return false;
      }
      case "error": {
        const { code, message } = parseErrorData(data);
        handlers.onError(code, message);
        return false;
      }
      case "done":
        handlers.onDone?.(parseDoneData(data));
        return true;
      default:
        return false;
    }
  };

  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });

    const lines = buffer.split("\n");
    buffer = lines.pop() ?? "";

    for (const line of lines) {
      if (line === "") {
        if (dispatch()) return;
        continue;
      }
      if (line.startsWith(":")) continue;
      if (line.startsWith("event:")) {
        currentEvent = line.slice("event:".length).trim();
        continue;
      }
      if (line.startsWith("data:")) {
        dataLines.push(line.slice("data:".length));
      }
    }
  }
}

function streamChat(
  body: { message: string; projectId?: string; envId?: string; appName?: string; mode: AgentMode },
  handlers: StreamChatHandlers
): Promise<void> {
  return streamSSE("/api/v1/agent/chat", body, handlers);
}

function streamConfirm(
  actionId: string,
  decision: "approve" | "reject",
  handlers: StreamChatHandlers
): Promise<void> {
  return streamSSE("/api/v1/agent/chat/confirm", { action_id: actionId, decision }, handlers);
}

interface HistoryMessage {
  role: "user" | "assistant" | "tool";
  content: string;
  toolName?: string;
}

interface HistoryResponse {
  messages: HistoryMessage[];
  pendingAction: ConfirmRequestPayload | null;
}

// fetchHistory reads back whatever the backend already persisted for this
// project/env scope (GET /agent/chat/history) so the panel can rebuild its
// message list after a page reload -- `messages` below is otherwise plain
// in-memory React state and a refresh throws it away even though the server
// remembers everything (agent_chat_messages + any still-open pending action).
async function fetchHistory(projectId?: string, envId?: string): Promise<HistoryResponse | null> {
  const token = await getToken();
  const params = new URLSearchParams();
  if (projectId) params.set("projectId", projectId);
  if (envId) params.set("envId", envId);
  const res = await fetch(`/api/v1/agent/chat/history?${params.toString()}`, {
    headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}) },
  });
  if (!res.ok) return null;
  return (await res.json()) as HistoryResponse;
}

// clearContext tells the backend to stop feeding everything before now into
// the next chat turn (and to reconstruct history from this point on),
// without deleting any underlying agent_chat_messages rows -- the daily
// message cap and the confirm/decline audit trail are computed from those
// rows and are completely unaffected by clearing.
async function clearContext(projectId?: string, envId?: string): Promise<boolean> {
  const token = await getToken();
  const res = await fetch("/api/v1/agent/chat/context/clear", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: JSON.stringify({ projectId, envId }),
  });
  return res.ok;
}

const PRESET_KEYS = ["deploy", "buildFailed", "billing"] as const;

interface AgentChatPanelProps {
  open: boolean;
  onClose: () => void;
}

export function AgentChatPanel({ open, onClose }: AgentChatPanelProps) {
  const { t } = useT();
  const { projectId, selectedEnv } = useProjectContext();
  const pathname = usePathname();
  const router = useRouter();
  const appName = appNameFromPath(pathname);

  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const mode = useSyncExternalStore(subscribeMode, readStoredMode, () => "edit" as AgentMode);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const listRef = useRef<HTMLDivElement>(null);

  // Restore the persisted conversation for this project/env scope. selectedEnv
  // resolves asynchronously (ProjectProvider fetches project details after its
  // own mount), so a one-shot "run once on mount" effect would fire with a
  // stale/undefined envId and silently hydrate the wrong (empty) scope. Instead
  // this re-fires whenever projectId/selectedEnv change, but only ever applies
  // its result while the panel is still showing nothing of its own (guarded via
  // the functional setMessages below) -- so it never clobbers a conversation
  // the user has actually started, no matter how many times project context
  // settles or changes.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      const history = await fetchHistory(projectId ?? undefined, selectedEnv?.id);
      if (!history || cancelled) return;

      const hydrated: ChatMessage[] = history.messages.map((m) =>
        m.role === "tool"
          ? { id: newId(), kind: "tool_call", name: m.toolName ?? "" }
          : { id: newId(), kind: "message", role: m.role, content: m.content }
      );
      if (history.pendingAction) {
        hydrated.push({
          id: newId(),
          kind: "confirm",
          actionId: history.pendingAction.actionId,
          toolName: history.pendingAction.toolName,
          args: history.pendingAction.args,
          summary: history.pendingAction.summary,
          priceRub: history.pendingAction.priceRub,
        });
      }
      if (hydrated.length === 0) return;
      setMessages((prev) => (prev.length === 0 ? hydrated : prev));
    })();

    return () => {
      cancelled = true;
    };
  }, [projectId, selectedEnv?.id]);

  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  useEffect(() => {
    listRef.current?.scrollTo({ top: listRef.current.scrollHeight });
  }, [messages]);

  function selectMode(next: AgentMode) {
    writeStoredMode(next);
  }

  const hasPendingConfirm = messages.some((m) => m.kind === "confirm" && !m.resolved);

  async function handleSend(preset?: string) {
    const text = (preset ?? input).trim();
    if (!text || sending || hasPendingConfirm) return;

    const userMsg: ChatMessage = { id: newId(), kind: "message", role: "user", content: text };
    let assistantId = newId();
    const assistantMsg: ChatMessage = { id: assistantId, kind: "message", role: "assistant", content: "", pending: true };

    setMessages((prev) => [...prev, userMsg, assistantMsg]);
    setInput("");
    setSending(true);

    let sawError = false;

    try {
      await streamChat(
        { message: text, projectId: projectId ?? undefined, envId: selectedEnv?.id, appName, mode },
        {
          onToken: (chunk) => {
            setMessages((prev) =>
              prev.map((m) =>
                m.id === assistantId && m.kind === "message" ? { ...m, content: m.content + chunk, pending: false } : m
              )
            );
          },
          onToolCall: (name) => {
            const nextAssistantId = newId();
            setMessages((prev) => [
              ...prev,
              { id: newId(), kind: "tool_call", name },
              { id: nextAssistantId, kind: "message", role: "assistant", content: "", pending: true },
            ]);
            assistantId = nextAssistantId;
          },
          onConfirmRequest: (req) => {
            setMessages((prev) => [
              ...prev,
              {
                id: newId(),
                kind: "confirm",
                actionId: req.actionId,
                toolName: req.toolName,
                args: req.args,
                summary: req.summary,
                priceRub: req.priceRub,
              },
            ]);
          },
          onNavigate: handleAgentNavigate,
          onError: (code, message) => {
            sawError = true;
            const key = AGENT_ERROR_CODE_KEYS[code];
            const displayMessage = key ? t(key) : message || t("agentChat.errorGeneric");
            setMessages((prev) => [
              ...prev.map((m) => (m.kind === "confirm" && !m.resolved ? { ...m, resolved: "error" as const } : m)),
              { id: newId(), kind: "error", code, message: displayMessage },
            ]);
          },
        }
      );
    } catch {
      if (!sawError) {
        setMessages((prev) => [
          ...prev.map((m) => (m.kind === "confirm" && !m.resolved ? { ...m, resolved: "error" as const } : m)),
          { id: newId(), kind: "error", code: "upstream", message: t("agentChat.errorGeneric") },
        ]);
      }
    } finally {
      setMessages((prev) =>
        prev
          .map((m) => (m.kind === "message" ? { ...m, pending: false } : m))
          .filter((m) => !(m.kind === "message" && m.role === "assistant" && m.content === ""))
      );
      setSending(false);
    }
  }

  function handleTogglePriceAck(actionId: string, acknowledged: boolean) {
    setMessages((prev) =>
      prev.map((m) => (m.kind === "confirm" && m.actionId === actionId ? { ...m, acknowledged } : m))
    );
  }

  async function handleConfirm(actionId: string, decision: "approve" | "reject") {
    if (sending) return;

    setMessages((prev) =>
      prev.map((m) =>
        m.kind === "confirm" && m.actionId === actionId
          ? { ...m, resolved: decision === "approve" ? "approved" : "rejected" }
          : m
      )
    );

    let assistantId = newId();
    const assistantMsg: ChatMessage = { id: assistantId, kind: "message", role: "assistant", content: "", pending: true };
    setMessages((prev) => [...prev, assistantMsg]);
    setSending(true);

    let sawError = false;

    try {
      await streamConfirm(actionId, decision, {
        onToken: (chunk) => {
          setMessages((prev) =>
            prev.map((m) =>
              m.id === assistantId && m.kind === "message" ? { ...m, content: m.content + chunk, pending: false } : m
            )
          );
        },
        onToolCall: (name) => {
          const nextAssistantId = newId();
          setMessages((prev) => [
            ...prev,
            { id: newId(), kind: "tool_call", name },
            { id: nextAssistantId, kind: "message", role: "assistant", content: "", pending: true },
          ]);
          assistantId = nextAssistantId;
        },
        onConfirmRequest: (req) => {
          setMessages((prev) => [
            ...prev,
            {
              id: newId(),
              kind: "confirm",
              actionId: req.actionId,
              toolName: req.toolName,
              args: req.args,
              summary: req.summary,
              priceRub: req.priceRub,
            },
          ]);
        },
        onNavigate: handleAgentNavigate,
        onError: (code, message) => {
          sawError = true;
          const key = AGENT_ERROR_CODE_KEYS[code];
          const displayMessage = key ? t(key) : message || t("agentChat.errorGeneric");
          setMessages((prev) => [...prev, { id: newId(), kind: "error", code, message: displayMessage }]);
        },
      });
    } catch {
      if (!sawError) {
        setMessages((prev) => [...prev, { id: newId(), kind: "error", code: "upstream", message: t("agentChat.errorGeneric") }]);
      }
    } finally {
      setMessages((prev) =>
        prev
          .map((m) => (m.kind === "message" ? { ...m, pending: false } : m))
          .filter((m) => !(m.kind === "message" && m.role === "assistant" && m.content === ""))
      );
      setSending(false);
    }
  }

  async function handleClearContext() {
    if (sending) return;
    const ok = await clearContext(projectId ?? undefined, selectedEnv?.id);
    if (ok) setMessages([]);
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  }

  /**
   * Takes the user to the page the assistant opened for them.
   *
   * The answer is still streaming while this runs, so it goes through the Next
   * router rather than a location assignment: a full page load would tear down
   * the fetch mid-turn and the user would land on the right page with half an
   * answer and no panel. Client-side routing keeps both, and Back still undoes
   * the move.
   */
  function handleAgentNavigate(path: string) {
    router.push(path);
  }

  /**
   * Routes clicks on console links the assistant produced through the Next
   * router. The answer HTML comes from dangerouslySetInnerHTML, so there is no
   * <Link> to attach to; a delegated handler on the container is what keeps a
   * suggested path from reloading the whole console and losing this panel.
   * Modified clicks and external hrefs keep the browser's own behaviour.
   */
  function handleMarkdownClick(e: React.MouseEvent<HTMLDivElement>) {
    if (e.defaultPrevented || e.button !== 0) return;
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const anchor = (e.target as HTMLElement).closest("a");
    if (!anchor) return;
    const href = anchor.getAttribute("href") ?? "";
    if (!isInternalConsolePath(href)) return;
    e.preventDefault();
    router.push(href);
  }

  return (
    <aside
      className={`absolute inset-y-0 right-0 z-40 flex w-full max-w-sm shrink-0 flex-col border-l border-gray-200 bg-white shadow-2xl transition-transform duration-200 dark:border-gray-800 dark:bg-gray-950 lg:static lg:w-[28vw] lg:min-w-[320px] lg:shadow-none ${
        open ? "translate-x-0" : "translate-x-full lg:hidden"
      }`}
    >
      <div className="flex items-center justify-between border-b border-gray-100 px-4 py-3 dark:border-gray-800">
        <div className="flex items-center gap-2">
          <Bot className="h-4 w-4 text-blue-600 dark:text-blue-400" />
          <span className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("agentChat.title")}</span>
        </div>
        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={handleClearContext}
            disabled={sending || messages.length === 0}
            aria-label={t("agentChat.clearContext")}
            title={t("agentChat.clearContext")}
            className="rounded-md p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 disabled:cursor-not-allowed disabled:opacity-40 dark:text-gray-500 dark:hover:bg-gray-900 dark:hover:text-gray-300"
          >
            <RotateCcw className="h-4 w-4" />
          </button>
          <button
            type="button"
            onClick={onClose}
            aria-label={t("agentChat.close")}
            className="rounded-md p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:text-gray-500 dark:hover:bg-gray-900 dark:hover:text-gray-300"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      </div>

      <div ref={listRef} className="flex-1 space-y-3 overflow-y-auto px-4 py-4">
        {messages.length === 0 && (
          <div className="space-y-3">
            <p className="text-sm text-gray-400 dark:text-gray-500">{t("agentChat.emptyState")}</p>
            <p className="text-xs text-gray-400 dark:text-gray-500">{t("agentChat.preset.hint")}</p>
            <div className="flex flex-wrap gap-2">
              {PRESET_KEYS.map((key) => (
                <button
                  key={key}
                  type="button"
                  onClick={() => handleSend(t(`agentChat.preset.${key}`))}
                  disabled={sending || hasPendingConfirm}
                  className="rounded-full border border-gray-200 px-3 py-1.5 text-xs text-gray-600 transition-colors hover:border-blue-400 hover:text-blue-600 disabled:cursor-not-allowed disabled:opacity-40 dark:border-gray-800 dark:text-gray-300 dark:hover:border-blue-500 dark:hover:text-blue-400"
                >
                  {t(`agentChat.preset.${key}`)}
                </button>
              ))}
            </div>
          </div>
        )}
        {messages.map((m) => {
          if (m.kind === "tool_call") {
            return (
              <div key={m.id} className="flex justify-start">
                <div className="inline-flex items-center gap-1.5 rounded-full bg-gray-50 px-2.5 py-1 text-xs text-gray-500 dark:bg-gray-900/60 dark:text-gray-400">
                  <Wrench className="h-3 w-3" />
                  {t("agentChat.toolCall", { name: m.name })}
                </div>
              </div>
            );
          }
          if (m.kind === "error") {
            return (
              <div key={m.id} className="flex justify-start">
                <div className="flex max-w-[85%] items-start gap-2 rounded-2xl border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-300">
                  <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                  <span>{m.message}</span>
                </div>
              </div>
            );
          }
          if (m.kind === "confirm") {
            const toolLabelKey = TOOL_NAME_KEYS[m.toolName];
            const toolLabel = toolLabelKey ? t(toolLabelKey) : m.toolName;
            const argEntries = confirmArgEntries(m.args, m.summary);
            const isPriced = typeof m.priceRub === "number" && m.priceRub > 0;
            const confirmBlocked = isPriced && !m.acknowledged;
            return (
              <div key={m.id} className="flex justify-start">
                <div className="w-full max-w-[92%] rounded-2xl border border-amber-200 bg-amber-50 px-3 py-2.5 text-sm dark:border-amber-900 dark:bg-amber-950/30">
                  <div className="mb-1 font-semibold text-amber-900 dark:text-amber-200">{t("agentChat.confirm.title")}</div>
                  <div className="text-amber-900 dark:text-amber-100">{toolLabel}</div>
                  {m.summary && <p className="mt-1 text-amber-800 dark:text-amber-200">{m.summary}</p>}
                  {argEntries.length > 0 && (
                    <ul className="mt-1.5 space-y-0.5 rounded-lg bg-white/60 px-2 py-1.5 font-mono text-xs text-amber-900 dark:bg-black/20 dark:text-amber-100">
                      {argEntries.map(([key, value]) => (
                        <li key={key} className="truncate">
                          <span className="text-amber-600 dark:text-amber-400">{key}:</span> {value}
                        </li>
                      ))}
                    </ul>
                  )}
                  {isPriced && (
                    <div className="mt-1.5 rounded-lg border border-amber-300 bg-white/70 px-2 py-1.5 dark:border-amber-800 dark:bg-black/20">
                      <div className="text-xs font-semibold text-amber-900 dark:text-amber-100">
                        {t("agentChat.confirm.priceEstimate", { price: formatRub(m.priceRub as number) })}
                      </div>
                      {!m.resolved && (
                        <label className="mt-1 flex items-start gap-1.5 text-xs text-amber-800 dark:text-amber-200">
                          <input
                            type="checkbox"
                            checked={!!m.acknowledged}
                            onChange={(e) => handleTogglePriceAck(m.actionId, e.target.checked)}
                            className="mt-0.5"
                          />
                          <span>{t("agentChat.confirm.priceAck")}</span>
                        </label>
                      )}
                    </div>
                  )}
                  {!m.resolved ? (
                    <div className="mt-2 flex gap-2">
                      <button
                        type="button"
                        onClick={() => handleConfirm(m.actionId, "approve")}
                        disabled={sending || confirmBlocked}
                        className="flex items-center gap-1 rounded-lg bg-amber-600 px-2.5 py-1 text-xs font-medium text-white hover:bg-amber-700 disabled:cursor-not-allowed disabled:opacity-40"
                      >
                        <Check className="h-3 w-3" />
                        {t("agentChat.confirm.approve")}
                      </button>
                      <button
                        type="button"
                        onClick={() => handleConfirm(m.actionId, "reject")}
                        disabled={sending}
                        className="flex items-center gap-1 rounded-lg border border-amber-300 px-2.5 py-1 text-xs font-medium text-amber-800 hover:bg-amber-100 disabled:cursor-not-allowed disabled:opacity-40 dark:border-amber-800 dark:text-amber-200 dark:hover:bg-amber-900/40"
                      >
                        <Ban className="h-3 w-3" />
                        {t("agentChat.confirm.reject")}
                      </button>
                    </div>
                  ) : (
                    <div className="mt-2 text-xs font-medium text-amber-700 dark:text-amber-300">
                      {m.resolved === "approved" && (sending ? t("agentChat.confirm.running") : t("agentChat.confirm.approved"))}
                      {m.resolved === "rejected" && t("agentChat.confirm.rejected")}
                      {m.resolved === "error" && t("agentChat.confirm.rejected")}
                    </div>
                  )}
                </div>
              </div>
            );
          }
          return (
            <div key={m.id} className={`flex ${m.role === "user" ? "justify-end" : "justify-start"}`}>
              <div
                className={`max-w-[85%] rounded-2xl px-3 py-2 text-sm ${
                  m.role === "user"
                    ? "whitespace-pre-wrap bg-blue-600 text-white"
                    : "bg-gray-100 text-gray-800 dark:bg-gray-900 dark:text-gray-200"
                }`}
              >
                {m.role === "assistant" && m.content ? (
                  <div
                    className="agent-chat-md"
                    onClick={handleMarkdownClick}
                    dangerouslySetInnerHTML={{ __html: renderMarkdown(autolinkConsolePaths(m.content)) }}
                  />
                ) : (
                  m.content
                )}
                {m.pending && m.content === "" && (
                  <span className="inline-flex items-center gap-1 text-gray-400 dark:text-gray-500">
                    <Loader2 className="h-3 w-3 animate-spin" />
                    {t("agentChat.thinking")}
                  </span>
                )}
              </div>
            </div>
          );
        })}
      </div>

      <div className="border-t border-gray-100 p-3 dark:border-gray-800">
        {hasPendingConfirm && (
          <p className="mb-2 text-xs text-amber-700 dark:text-amber-300">{t("agentChat.confirm.blockedHint")}</p>
        )}
        <div className="mb-2 flex items-center gap-2">
          <label htmlFor="agent-chat-mode" className="text-xs text-gray-500 dark:text-gray-400">
            {t("agentChat.mode.label")}
          </label>
          <select
            id="agent-chat-mode"
            value={mode}
            onChange={(e) => selectMode(e.target.value as AgentMode)}
            title={t(`agentChat.mode.${mode}Hint`)}
            className="rounded-md border border-gray-200 bg-white px-2 py-1 text-xs text-gray-700 focus:border-blue-400 focus:outline-none dark:border-gray-800 dark:bg-gray-900 dark:text-gray-200"
          >
            {AGENT_MODES.map((m) => (
              <option key={m} value={m}>
                {t(`agentChat.mode.${m}`)}
              </option>
            ))}
          </select>
        </div>
        <div className="flex items-end gap-2">
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={t("agentChat.placeholder")}
            rows={2}
            disabled={hasPendingConfirm}
            className="flex-1 resize-none rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 focus:border-blue-400 focus:outline-none disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-800 dark:bg-gray-900 dark:text-gray-100"
          />
          <button
            type="button"
            onClick={() => handleSend()}
            disabled={sending || !input.trim() || hasPendingConfirm}
            aria-label={t("agentChat.send")}
            className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-blue-600 text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-40"
          >
            <Send className="h-4 w-4" />
          </button>
        </div>
      </div>
    </aside>
  );
}

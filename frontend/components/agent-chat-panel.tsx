"use client";

import { useEffect, useRef, useState } from "react";
import { usePathname } from "next/navigation";
import { X, Send, Bot, Loader2 } from "lucide-react";
import { useT } from "@/lib/i18n/console/context";
import { useProjectContext } from "@/lib/project-context";
import { getToken } from "@/lib/api";

interface ChatMessage {
  id: string;
  role: "user" | "assistant";
  content: string;
  pending?: boolean;
}

function appNameFromPath(pathname: string): string | undefined {
  const segs = pathname.split("/").filter(Boolean);
  const idx = segs.indexOf("apps");
  return idx >= 0 && segs[idx + 1] ? decodeURIComponent(segs[idx + 1]) : undefined;
}

function newId(): string {
  return `${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;
}

async function streamChat(
  body: { message: string; projectId?: string; envId?: string; appName?: string },
  onToken: (chunk: string) => void
): Promise<void> {
  const token = await getToken();
  const res = await fetch("/api/v1/agent/chat", {
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

  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });

    const lines = buffer.split("\n");
    buffer = lines.pop() ?? "";

    for (const line of lines) {
      if (line.startsWith("event:")) {
        currentEvent = line.slice("event:".length).trim();
        continue;
      }
      if (line.startsWith("data:")) {
        const data = line.slice("data:".length).trim();
        if (currentEvent === "token") onToken(data);
        if (currentEvent === "done") return;
      }
    }
  }
}

interface AgentChatPanelProps {
  open: boolean;
  onClose: () => void;
}

export function AgentChatPanel({ open, onClose }: AgentChatPanelProps) {
  const { t } = useT();
  const { projectId, selectedEnv } = useProjectContext();
  const pathname = usePathname();
  const appName = appNameFromPath(pathname);

  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const listRef = useRef<HTMLDivElement>(null);

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

  async function handleSend() {
    const text = input.trim();
    if (!text || sending) return;

    const userMsg: ChatMessage = { id: newId(), role: "user", content: text };
    const assistantId = newId();
    const assistantMsg: ChatMessage = { id: assistantId, role: "assistant", content: "", pending: true };

    setMessages((prev) => [...prev, userMsg, assistantMsg]);
    setInput("");
    setSending(true);

    try {
      await streamChat(
        { message: text, projectId: projectId ?? undefined, envId: selectedEnv?.id, appName },
        (chunk) => {
          setMessages((prev) =>
            prev.map((m) => (m.id === assistantId ? { ...m, content: m.content + chunk, pending: false } : m))
          );
        }
      );
      setMessages((prev) => prev.map((m) => (m.id === assistantId ? { ...m, pending: false } : m)));
    } catch {
      setMessages((prev) =>
        prev.map((m) =>
          m.id === assistantId ? { ...m, content: t("agentChat.errorGeneric"), pending: false } : m
        )
      );
    } finally {
      setSending(false);
    }
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
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
        <button
          type="button"
          onClick={onClose}
          aria-label={t("agentChat.close")}
          className="rounded-md p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:text-gray-500 dark:hover:bg-gray-900 dark:hover:text-gray-300"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      <div ref={listRef} className="flex-1 space-y-3 overflow-y-auto px-4 py-4">
        {messages.length === 0 && (
          <p className="text-sm text-gray-400 dark:text-gray-500">{t("agentChat.emptyState")}</p>
        )}
        {messages.map((m) => (
          <div key={m.id} className={`flex ${m.role === "user" ? "justify-end" : "justify-start"}`}>
            <div
              className={`max-w-[85%] whitespace-pre-wrap rounded-2xl px-3 py-2 text-sm ${
                m.role === "user"
                  ? "bg-blue-600 text-white"
                  : "bg-gray-100 text-gray-800 dark:bg-gray-900 dark:text-gray-200"
              }`}
            >
              {m.content}
              {m.pending && m.content === "" && (
                <span className="inline-flex items-center gap-1 text-gray-400 dark:text-gray-500">
                  <Loader2 className="h-3 w-3 animate-spin" />
                  {t("agentChat.thinking")}
                </span>
              )}
            </div>
          </div>
        ))}
      </div>

      <div className="border-t border-gray-100 p-3 dark:border-gray-800">
        <div className="flex items-end gap-2">
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={t("agentChat.placeholder")}
            rows={2}
            className="flex-1 resize-none rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 focus:border-blue-400 focus:outline-none dark:border-gray-800 dark:bg-gray-900 dark:text-gray-100"
          />
          <button
            type="button"
            onClick={handleSend}
            disabled={sending || !input.trim()}
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

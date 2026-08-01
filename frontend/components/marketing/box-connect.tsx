"use client";

import { useState } from "react";
import { clsx } from "clsx";
import { CopyButton } from "@/components/ui/copy-button";
import type { BoxCopy } from "@/lib/box-copy";

type ConnectCopy = BoxCopy["connect"];

/**
 * "Connect in 60 seconds" — the section that replaces "join a waitlist" with the
 * path that actually works today: box up, agent attaches, done. Sits right after
 * "How it works" and before the demo, so the visitor sees the real command before
 * the scripted replay.
 */
export function BoxConnect({ copy, helpHref }: { copy: ConnectCopy; helpHref: string }) {
  const [tab, setTab] = useState<"claude" | "other">("claude");

  return (
    <section id="connect" className="scroll-mt-20 bg-white py-20">
      <div className="mx-auto max-w-4xl px-4 sm:px-6 lg:px-8">
        <div className="mb-10 max-w-2xl">
          <h2 className="text-3xl font-bold tracking-tight text-slate-900">{copy.title}</h2>
          <p className="mt-3 text-lg text-slate-600">{copy.subtitle}</p>
        </div>

        <div className="inline-flex gap-1 rounded-xl border border-slate-200 bg-slate-50 p-1">
          <TabButton active={tab === "claude"} onClick={() => setTab("claude")}>
            {copy.tabClaude}
          </TabButton>
          <TabButton active={tab === "other"} onClick={() => setTab("other")}>
            {copy.tabOther}
          </TabButton>
        </div>

        {tab === "claude" ? (
          <div className="mt-6 space-y-4">
            <CommandRow
              label={copy.claudeStep1Label}
              cmd={copy.claudeStep1Cmd}
              copyLabel={copy.copyLabel}
              uxId="claude_code_1"
            />
            <CommandRow
              label={copy.claudeStep2Label}
              cmd={copy.claudeStep2Cmd}
              copyLabel={copy.copyLabel}
              uxId="claude_code_2"
            />
          </div>
        ) : (
          <div className="mt-6 space-y-3">
            <p className="text-sm text-slate-600">{copy.otherLabel}</p>
            <div className="flex items-start gap-3 rounded-lg border border-slate-800 bg-slate-900 p-4">
              <pre className="flex-1 overflow-x-auto font-mono text-xs leading-relaxed text-slate-100">
                {copy.otherCmd}
              </pre>
              <span data-ux="box_connect:json" className="shrink-0">
                <CopyButton
                  value={copy.otherCmd}
                  label={copy.copyLabel}
                  className="border-white/20 bg-white/10 text-white hover:bg-white/20"
                />
              </span>
            </div>
            <p className="text-sm text-slate-500">{copy.otherNote}</p>
          </div>
        )}

        <p className="mt-8 text-sm text-slate-500">
          {copy.footNote}{" "}
          <a href={helpHref} className="font-medium text-blue-600 hover:underline">
            {copy.helpLink}
          </a>
        </p>
      </div>
    </section>
  );
}

function TabButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={clsx(
        "rounded-lg px-4 py-2 text-sm font-semibold transition-colors",
        active ? "bg-white text-slate-900 shadow-sm" : "text-slate-500 hover:text-slate-700",
      )}
    >
      {children}
    </button>
  );
}

function CommandRow({
  label,
  cmd,
  copyLabel,
  uxId,
}: {
  label: string;
  cmd: string;
  copyLabel: string;
  uxId: string;
}) {
  return (
    <div>
      <p className="mb-1.5 text-sm font-medium text-slate-700">{label}</p>
      <div className="flex items-center gap-3 rounded-lg border border-slate-800 bg-slate-900 px-4 py-3">
        <code className="flex-1 overflow-x-auto whitespace-nowrap font-mono text-xs text-slate-100">
          {cmd}
        </code>
        <span data-ux={`box_connect:${uxId}`} className="shrink-0">
          <CopyButton
            value={cmd}
            label={copyLabel}
            className="border-white/20 bg-white/10 text-white hover:bg-white/20"
          />
        </span>
      </div>
    </div>
  );
}

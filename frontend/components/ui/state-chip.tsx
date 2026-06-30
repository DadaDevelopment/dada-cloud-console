import * as React from "react";
import { clsx } from "clsx";

/**
 * Project state-system chip. The redesign spec defines a small, explicit set of
 * states that should read at a glance on the overview: Ready, Needs action,
 * Error, Protected, Backup enabled — plus a neutral variant for plain counts
 * (e.g. "1 app", "2 db").
 */
export type ChipTone = "ready" | "needs-action" | "error" | "protected" | "backup" | "neutral";

const toneMap: Record<ChipTone, string> = {
  ready: "bg-green-100 dark:bg-green-950/40 text-green-700 dark:text-green-300",
  "needs-action": "bg-amber-100 dark:bg-amber-950/40 text-amber-700 dark:text-amber-300",
  error: "bg-red-100 dark:bg-red-950/40 text-red-700 dark:text-red-300",
  protected: "bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-300",
  backup: "bg-blue-100 dark:bg-blue-950/40 text-blue-700 dark:text-blue-300",
  neutral: "bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-400",
};

const dotMap: Record<ChipTone, string> = {
  ready: "bg-green-500",
  "needs-action": "bg-amber-500",
  error: "bg-red-500",
  protected: "bg-slate-400",
  backup: "bg-blue-500",
  neutral: "bg-gray-400 dark:bg-gray-500",
};

export function StateChip({
  tone = "neutral",
  dot = false,
  children,
}: {
  tone?: ChipTone;
  dot?: boolean;
  children: React.ReactNode;
}) {
  return (
    <span
      className={clsx(
        "inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium",
        toneMap[tone]
      )}
    >
      {dot && <span className={clsx("h-1.5 w-1.5 rounded-full", dotMap[tone])} />}
      {children}
    </span>
  );
}

"use client";

export type FunnelStepRow = { key: string; label: string; count: number; tone?: "error" };

/** Centred tapering steps so a sequence of stages reads as one funnel:
 * bar width is the share of the widest stage, and each step carries the
 * conversion from the step above it, which is the number an operator
 * actually acts on. */
export function FunnelSteps({ rows, showConversion = true }: { rows: FunnelStepRow[]; showConversion?: boolean }) {
  const max = rows.length ? Math.max(1, ...rows.map((r) => r.count)) : 1;
  return (
    <div className="mx-auto w-full max-w-[720px] space-y-1">
      {rows.map((r, i) => {
        const pct = Math.max(r.count > 0 ? 6 : 2, (r.count / max) * 100);
        const prev = i > 0 ? rows[i - 1].count : null;
        const step = prev && prev > 0 ? Math.round((r.count / prev) * 100) : null;
        return (
          <div key={r.key} className="flex items-center gap-3">
            <div className="w-40 shrink-0 truncate text-right text-xs font-medium text-gray-500 dark:text-gray-400">
              {r.label}
            </div>
            <div className="flex flex-1 justify-center">
              <div
                className={`flex h-7 items-center justify-center rounded-sm text-xs font-semibold text-white transition-all ${
                  r.tone === "error" ? "bg-red-500 dark:bg-red-600" : "bg-blue-500 dark:bg-blue-600"
                }`}
                style={{ width: `${pct}%` }}
              >
                {r.count > 0 && pct > 12 ? r.count : null}
              </div>
            </div>
            <div className="w-24 shrink-0 text-xs tabular-nums text-gray-500 dark:text-gray-400">
              {r.count > 0 && pct <= 12 ? (
                <span className="font-semibold text-gray-900 dark:text-gray-100">{r.count}</span>
              ) : null}
              {showConversion && step !== null ? <span className="ml-1">{step}%</span> : null}
            </div>
          </div>
        );
      })}
    </div>
  );
}

"use client";
import { RefreshCw, SlidersHorizontal, Plus, Settings2, Check } from "lucide-react";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "@/components/ui/select";
import { Popover, PopoverTrigger, PopoverContent } from "@/components/ui/popover";
import {
  AGG_OPTIONS,
  RANGE_PRESETS,
  REFRESH_OPTIONS,
  type DashboardState,
} from "./dashboard-types";
import type { MonitoringLabelsResponse } from "@/lib/types";
import { cn } from "@/lib/cn";

type Update = (patch: Partial<DashboardState> | ((s: DashboardState) => DashboardState)) => void;

function toLocalInput(unix?: number): string {
  if (!unix) return "";
  const d = new Date(unix * 1000);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function Toolbar({
  state,
  update,
  labels,
  loading,
  editable,
  onToggleEdit,
  onRefresh,
  onAddPanel,
}: {
  state: DashboardState;
  update: Update;
  labels: MonitoringLabelsResponse;
  loading: boolean;
  editable: boolean;
  onToggleEdit: () => void;
  onRefresh: () => void;
  onAddPanel: () => void;
}) {
  const labelKeys = Object.keys(labels.labels).sort();
  const activeFilters = Object.keys(state.filters).length;

  return (
    <div className="flex flex-wrap items-center gap-2 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-2 shadow-sm">
      {/* Time range */}
      <Select
        value={RANGE_PRESETS.includes(state.range as never) ? state.range : "custom"}
        onValueChange={(v) => {
          if (v === "custom") update({ range: "custom" });
          else update({ range: v, from: undefined, to: undefined });
        }}
      >
        <SelectTrigger className="w-28">
          <span className="text-gray-400 dark:text-gray-500">⏱</span>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {RANGE_PRESETS.map((r) => (
            <SelectItem key={r} value={r}>
              Last {r}
            </SelectItem>
          ))}
          <SelectItem value="custom">Custom…</SelectItem>
        </SelectContent>
      </Select>

      {state.range === "custom" && (
        <Popover>
          <PopoverTrigger asChild>
            <button className="h-8 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-2.5 text-xs font-medium text-gray-600 dark:text-gray-400 shadow-sm hover:border-gray-300">
              {state.from && state.to
                ? `${toLocalInput(state.from).replace("T", " ")} → ${toLocalInput(state.to).replace("T", " ")}`
                : "Pick window"}
            </button>
          </PopoverTrigger>
          <PopoverContent className="w-72 space-y-2">
            <label className="block text-xs font-medium text-gray-500 dark:text-gray-400">
              From
              <input
                type="datetime-local"
                value={toLocalInput(state.from)}
                onChange={(e) =>
                  update({ from: e.target.value ? Math.floor(new Date(e.target.value).getTime() / 1000) : undefined })
                }
                className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-2 py-1 text-sm"
              />
            </label>
            <label className="block text-xs font-medium text-gray-500 dark:text-gray-400">
              To
              <input
                type="datetime-local"
                value={toLocalInput(state.to)}
                onChange={(e) =>
                  update({ to: e.target.value ? Math.floor(new Date(e.target.value).getTime() / 1000) : undefined })
                }
                className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-2 py-1 text-sm"
              />
            </label>
          </PopoverContent>
        </Popover>
      )}

      {/* Refresh interval */}
      <Select value={String(state.refreshMs)} onValueChange={(v) => update({ refreshMs: Number(v) })}>
        <SelectTrigger className="w-24">
          <RefreshCw className="h-3.5 w-3.5 text-gray-400 dark:text-gray-500" />
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {REFRESH_OPTIONS.map((o) => (
            <SelectItem key={o.value} value={String(o.value)}>
              {o.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      <div className="mx-1 h-5 w-px bg-gray-200 dark:bg-gray-700" />

      {/* Group by */}
      <Select value={state.groupBy || "__none"} onValueChange={(v) => update({ groupBy: v === "__none" ? "" : v })}>
        <SelectTrigger className="w-32">
          <span className="text-gray-400 dark:text-gray-500">∑</span>
          <SelectValue placeholder="Group by" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="__none">No group by</SelectItem>
          {labelKeys.map((k) => (
            <SelectItem key={k} value={k}>
              by {k}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      {/* Aggregation */}
      <Select value={state.agg || ""} onValueChange={(v) => update({ agg: v })}>
        <SelectTrigger className="w-28">
          <span className="text-gray-400 dark:text-gray-500">ƒ</span>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {AGG_OPTIONS.map((o) => (
            <SelectItem key={o.value} value={o.value}>
              {o.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      {/* Filters */}
      {labelKeys.length > 0 && (
        <Popover>
          <PopoverTrigger asChild>
            <button
              className={cn(
                "inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-xs font-medium shadow-sm transition-colors",
                activeFilters
                  ? "border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/40 text-blue-700 dark:text-blue-300"
                  : "border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 text-gray-600 dark:text-gray-400 hover:border-gray-300",
              )}
            >
              <SlidersHorizontal className="h-3.5 w-3.5" />
              Filters{activeFilters ? ` (${activeFilters})` : ""}
            </button>
          </PopoverTrigger>
          <PopoverContent className="w-72 space-y-2">
            <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">Label filters</p>
            {labelKeys.map((k) => (
              <label key={k} className="block text-xs font-medium text-gray-500 dark:text-gray-400">
                {k}
                <select
                  value={state.filters[k] ?? ""}
                  onChange={(e) =>
                    update((s) => {
                      const next = { ...s.filters };
                      if (e.target.value === "") delete next[k];
                      else next[k] = e.target.value;
                      return { ...s, filters: next };
                    })
                  }
                  className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-2 py-1 text-sm text-gray-900 dark:text-gray-100"
                >
                  <option value="">All</option>
                  {labels.labels[k].map((v) => (
                    <option key={v} value={v}>
                      {v}
                    </option>
                  ))}
                </select>
              </label>
            ))}
          </PopoverContent>
        </Popover>
      )}

      <div className="ml-auto flex items-center gap-2">
        <button
          onClick={onRefresh}
          title="Refresh now"
          className="flex h-8 w-8 items-center justify-center rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 text-gray-500 dark:text-gray-400 shadow-sm hover:border-gray-300 hover:text-gray-700"
        >
          <RefreshCw className={cn("h-3.5 w-3.5", loading && "animate-spin")} />
        </button>
        <button
          onClick={onToggleEdit}
          className={cn(
            "inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-xs font-medium shadow-sm transition-colors",
            editable
              ? "border-blue-600 bg-blue-600 text-white"
              : "border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 text-gray-600 dark:text-gray-400 hover:border-gray-300",
          )}
        >
          {editable ? <Check className="h-3.5 w-3.5" /> : <Settings2 className="h-3.5 w-3.5" />}
          {editable ? "Done" : "Edit"}
        </button>
        <button
          onClick={onAddPanel}
          className="inline-flex h-8 items-center gap-1.5 rounded-lg bg-gray-900 px-3 text-xs font-medium text-white shadow-sm hover:bg-gray-800"
        >
          <Plus className="h-3.5 w-3.5" /> Add panel
        </button>
      </div>
    </div>
  );
}

"use client";
import { useMemo, useState } from "react";
import {
  MoreHorizontal,
  Maximize2,
  RefreshCw,
  Download,
  Pencil,
  Trash2,
  GripVertical,
  X,
} from "lucide-react";
import { EChart } from "@/components/charts/echart";
import { dispatchBuild } from "@/components/charts/builders";
import { inferUnit } from "@/components/charts/format";
import { VIZ_LABELS } from "@/components/charts/types";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import { Spinner } from "@/components/ui/spinner";
import { useMetricsQuery } from "./use-metrics-query";
import type { PanelConfig } from "./dashboard-types";
import type { MonitoringMetricSpec } from "@/lib/types";
import { cn } from "@/lib/cn";

export interface PanelContext {
  projectId: string;
  envId: string;
  appId: string;
  range?: string;
  from?: number;
  to?: number;
  globalGroupBy: string;
  globalAgg: string;
  filters: string[];
  refreshMs: number;
  wideRange: boolean;
  /** Shared response keyed by metric, used when a panel has no overrides. */
  shared: Record<string, MonitoringMetricSpec> | undefined;
  sharedLoading: boolean;
}

/**
 * usePanelSpec resolves the metric series for a panel. Panels with a group-by or
 * aggregation override run their own scoped query; the rest read from the
 * dashboard's shared response so N plain panels cost one request.
 */
function usePanelSpec(panel: PanelConfig, ctx: PanelContext): { spec: MonitoringMetricSpec | undefined; loading: boolean; refresh: () => void } {
  const hasOverride = panel.groupBy !== undefined || panel.agg !== undefined;
  const own = useMetricsQuery({
    projectId: ctx.projectId,
    envId: ctx.envId,
    appId: ctx.appId,
    range: ctx.range,
    from: ctx.from,
    to: ctx.to,
    groupBy: panel.groupBy ?? ctx.globalGroupBy,
    agg: panel.agg ?? ctx.globalAgg,
    filters: ctx.filters,
    refreshMs: hasOverride ? ctx.refreshMs : 0,
  });
  if (hasOverride) {
    return { spec: own.data?.metrics?.[panel.metric], loading: own.loading, refresh: own.refresh };
  }
  return { spec: ctx.shared?.[panel.metric], loading: ctx.sharedLoading, refresh: () => {} };
}

function toCsv(spec: MonitoringMetricSpec, metric: string): string {
  const rows: string[] = ["timestamp,series,value"];
  for (const s of spec.series) {
    for (const p of s.points) {
      rows.push(`${new Date(p.t * 1000).toISOString()},${s.label || metric},${p.v}`);
    }
  }
  return rows.join("\n");
}

function download(name: string, content: string) {
  const blob = new Blob([content], { type: "text/csv" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}

export function Panel({
  panel,
  ctx,
  editable,
  onEdit,
  onRemove,
}: {
  panel: PanelConfig;
  ctx: PanelContext;
  editable: boolean;
  onEdit: (p: PanelConfig) => void;
  onRemove: (id: string) => void;
}) {
  const { spec, loading, refresh } = usePanelSpec(panel, ctx);
  const [full, setFull] = useState(false);
  const unit = spec?.unit || inferUnit(panel.metric);
  const hasData = (spec?.series?.length ?? 0) > 0 && spec!.series.some((s) => s.points.length > 0);

  const option = useMemo(() => {
    if (!spec) return null;
    return dispatchBuild({
      series: spec.series,
      viz: panel.viz,
      unit,
      thresholds: panel.thresholds,
      annotations: panel.annotations,
      wideRange: ctx.wideRange,
      zoom: panel.viz !== "gauge" && panel.viz !== "sparkline",
    });
  }, [spec, panel.viz, panel.thresholds, panel.annotations, unit, ctx.wideRange]);

  const title = panel.title || panel.metric;

  const chart =
    loading && !spec ? (
      <div className="flex h-full items-center justify-center">
        <Spinner size="md" />
      </div>
    ) : !hasData || !option ? (
      <div className="flex h-full flex-col items-center justify-center gap-1 text-center">
        <p className="text-xs font-medium text-gray-400 dark:text-gray-500">No data in range</p>
      </div>
    ) : (
      <EChart option={option} height="100%" aria-label={title} />
    );

  return (
    <div className="group flex h-full flex-col overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm transition-shadow hover:shadow-md dark:border-gray-800 dark:bg-gray-900">
      <div className="panel-drag flex cursor-move items-center justify-between gap-2 border-b border-gray-100 px-3 py-2 dark:border-gray-800">
        <div className="flex min-w-0 items-center gap-1.5">
          {editable && <GripVertical className="h-3.5 w-3.5 shrink-0 text-gray-300 dark:text-gray-600" />}
          <div className="min-w-0">
            <p className="truncate font-mono text-xs font-semibold text-gray-800 dark:text-gray-200">{title}</p>
            <p className="truncate text-[10px] uppercase tracking-wide text-gray-400 dark:text-gray-500">
              {VIZ_LABELS[panel.viz]}
              {(panel.agg ?? ctx.globalAgg) ? ` · ${panel.agg ?? ctx.globalAgg}` : ""}
              {(panel.groupBy ?? ctx.globalGroupBy) ? ` · by ${panel.groupBy ?? ctx.globalGroupBy}` : ""}
            </p>
          </div>
        </div>
        <div
          className="flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100"
          onMouseDown={(e) => e.stopPropagation()}
        >
          <IconBtn title="Refresh" onClick={refresh}>
            <RefreshCw className="h-3.5 w-3.5" />
          </IconBtn>
          <IconBtn title="Fullscreen" onClick={() => setFull(true)}>
            <Maximize2 className="h-3.5 w-3.5" />
          </IconBtn>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                className="flex h-6 w-6 items-center justify-center rounded-md text-gray-400 hover:bg-gray-100 hover:text-gray-700 dark:text-gray-500 dark:hover:bg-gray-800 dark:hover:text-gray-300"
                title="More"
              >
                <MoreHorizontal className="h-3.5 w-3.5" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              {editable && (
                <DropdownMenuItem onSelect={() => onEdit(panel)}>
                  <Pencil className="h-3.5 w-3.5" /> Edit panel
                </DropdownMenuItem>
              )}
              <DropdownMenuItem
                onSelect={() => spec && download(`${panel.metric}.csv`, toCsv(spec, panel.metric))}
              >
                <Download className="h-3.5 w-3.5" /> Export CSV
              </DropdownMenuItem>
              {editable && (
                <>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem destructive onSelect={() => onRemove(panel.id)}>
                    <Trash2 className="h-3.5 w-3.5" /> Remove
                  </DropdownMenuItem>
                </>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>
      <div className="min-h-0 flex-1 p-2">{chart}</div>

      {full && (
        <div className="fixed inset-0 z-50 flex flex-col bg-black/50 backdrop-blur-sm" onClick={() => setFull(false)}>
          <div
            className="m-auto flex h-[80vh] w-[90vw] flex-col overflow-hidden rounded-2xl bg-white shadow-2xl dark:bg-gray-900"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-center justify-between border-b border-gray-100 px-5 py-3 dark:border-gray-800">
              <p className="font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{title}</p>
              <button
                onClick={() => setFull(false)}
                className="flex h-7 w-7 items-center justify-center rounded-md text-gray-400 hover:bg-gray-100 dark:text-gray-500 dark:hover:bg-gray-800"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="flex-1 p-4">{option && <EChart option={option} height="100%" aria-label={title} />}</div>
          </div>
        </div>
      )}
    </div>
  );
}

function IconBtn({ title, onClick, children }: { title: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      title={title}
      onClick={onClick}
      className={cn("flex h-6 w-6 items-center justify-center rounded-md text-gray-400 hover:bg-gray-100 hover:text-gray-700 dark:text-gray-500 dark:hover:bg-gray-800 dark:hover:text-gray-300")}
    >
      {children}
    </button>
  );
}

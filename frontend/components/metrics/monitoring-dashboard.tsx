"use client";
import { useCallback, useEffect, useMemo, useState } from "react";
import { LayoutDashboard, Compass } from "lucide-react";
import { monitoringApi } from "@/lib/api";
import type { MonitoringLabelsResponse } from "@/lib/types";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Spinner } from "@/components/ui/spinner";
import { useDashboardState } from "./use-dashboard-state";
import { useMetricsQuery } from "./use-metrics-query";
import { rangeParams, isWideRange, type PanelConfig } from "./dashboard-types";
import { Toolbar } from "./toolbar";
import { KpiRow } from "./kpi-row";
import { PanelGrid, type GridLayoutItem } from "./panel-grid";
import { MetricsExplorer } from "./metrics-explorer";
import { AddPanelDialog } from "./add-panel-dialog";
import type { PanelContext } from "./panel";

const PANEL_W = 6;
const PANEL_H = 7;

function newId(seed: string, n: number): string {
  return `${seed}-${n}-${Math.random().toString(36).slice(2, 8)}`;
}

/** Lays auto-seeded panels two per row across the 12-col grid. */
function seedLayout(metrics: string[], seed: string): PanelConfig[] {
  return metrics.map((m, i) => ({
    id: newId(seed, i),
    metric: m,
    viz: "line",
    x: (i % 2) * PANEL_W,
    y: Math.floor(i / 2) * PANEL_H,
    w: PANEL_W,
    h: PANEL_H,
  }));
}

/**
 * MonitoringDashboard is the enterprise observability surface for one monitoring
 * resource: a toolbar (time/refresh/group-by/aggregation/filters), a KPI strip,
 * a draggable/resizable panel grid, a metrics explorer, and a panel editor — all
 * persisted to localStorage. Replaces the old fixed MetricsPanel grid.
 */
export function MonitoringDashboard({
  projectId,
  envId,
  appId,
}: {
  projectId: string;
  envId: string;
  appId: string;
}) {
  const [state, update] = useDashboardState(projectId, envId, appId);
  const [labels, setLabels] = useState<MonitoringLabelsResponse>({ labels: {}, names: [] });
  const [editable, setEditable] = useState(false);
  const [tab, setTab] = useState("dashboard");
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingPanel, setEditingPanel] = useState<PanelConfig | null>(null);

  const filterList = useMemo(
    () => Object.entries(state.filters).map(([k, v]) => `${k}=${v}`),
    [state.filters],
  );
  const rp = rangeParams(state);

  const { data, loading, error, refresh } = useMetricsQuery({
    projectId,
    envId,
    appId,
    range: rp.range,
    from: rp.from,
    to: rp.to,
    groupBy: state.groupBy,
    agg: state.agg,
    filters: filterList,
    refreshMs: state.refreshMs,
  });

  useEffect(() => {
    monitoringApi
      .getLabels(projectId, envId, appId, "24h")
      .then(setLabels)
      .catch(() => setLabels({ labels: {}, names: [] }));
  }, [projectId, envId, appId]);

  const metricNames = useMemo(() => Object.keys(data?.metrics ?? {}).sort(), [data]);

  // Auto-seed a starter dashboard the first time metrics are discovered.
  useEffect(() => {
    if (state.initialized || metricNames.length === 0) return;
    update((s) => ({ ...s, panels: seedLayout(metricNames, appId), initialized: true }));
  }, [state.initialized, metricNames, appId, update]);

  const ctx: PanelContext = useMemo(
    () => ({
      projectId,
      envId,
      appId,
      range: rp.range,
      from: rp.from,
      to: rp.to,
      globalGroupBy: state.groupBy,
      globalAgg: state.agg,
      filters: filterList,
      refreshMs: state.refreshMs,
      wideRange: isWideRange(state),
      shared: data?.metrics,
      sharedLoading: loading,
    }),
    [projectId, envId, appId, rp.range, rp.from, rp.to, state, filterList, data, loading],
  );

  const onLayoutChange = useCallback(
    (layout: GridLayoutItem[]) => {
      update((s) => ({
        ...s,
        panels: s.panels.map((p) => {
          const l = layout.find((x) => x.i === p.id);
          return l ? { ...p, x: l.x, y: l.y, w: l.w, h: l.h } : p;
        }),
      }));
    },
    [update],
  );

  const addOrSavePanel = useCallback(
    (p: Omit<PanelConfig, "id" | "x" | "y" | "w" | "h"> & Partial<Pick<PanelConfig, "id">>) => {
      update((s) => {
        if (p.id) {
          return { ...s, panels: s.panels.map((x) => (x.id === p.id ? { ...x, ...p, id: x.id } : x)) };
        }
        const maxY = s.panels.reduce((m, x) => Math.max(m, x.y + x.h), 0);
        const panel: PanelConfig = {
          id: newId(appId, s.panels.length),
          metric: p.metric,
          viz: p.viz,
          title: p.title,
          groupBy: p.groupBy,
          agg: p.agg,
          thresholds: p.thresholds,
          annotations: p.annotations,
          x: 0,
          y: maxY,
          w: PANEL_W,
          h: PANEL_H,
        };
        return { ...s, panels: [...s.panels, panel], initialized: true };
      });
    },
    [appId, update],
  );

  const quickAdd = useCallback(
    (metric: string) => {
      addOrSavePanel({ metric, viz: "line" });
      setTab("dashboard");
    },
    [addOrSavePanel],
  );

  const removePanel = useCallback(
    (id: string) => update((s) => ({ ...s, panels: s.panels.filter((p) => p.id !== id) })),
    [update],
  );

  const openEdit = useCallback((p: PanelConfig) => {
    setEditingPanel(p);
    setDialogOpen(true);
  }, []);

  return (
    <div className="space-y-4">
      <Tabs value={tab} onValueChange={setTab}>
        <div className="flex items-center justify-between gap-3">
          <TabsList>
            <TabsTrigger value="dashboard">
              <LayoutDashboard className="mr-1.5 h-3.5 w-3.5" /> Dashboard
            </TabsTrigger>
            <TabsTrigger value="explore">
              <Compass className="mr-1.5 h-3.5 w-3.5" /> Explore
            </TabsTrigger>
          </TabsList>
          {data?.live_error && (
            <span title={data.live_error} className="text-xs text-amber-600">
              partial data
            </span>
          )}
        </div>

        <div className="mt-4">
          <Toolbar
            state={state}
            update={update}
            labels={labels}
            loading={loading}
            editable={editable}
            onToggleEdit={() => setEditable((v) => !v)}
            onRefresh={refresh}
            onAddPanel={() => {
              setEditingPanel(null);
              setDialogOpen(true);
            }}
          />
        </div>

        <TabsContent value="dashboard">
          {error ? (
            <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-600">{error}</div>
          ) : loading && !data ? (
            <div className="flex h-48 items-center justify-center">
              <Spinner size="lg" />
            </div>
          ) : metricNames.length === 0 ? (
            <EmptyState />
          ) : (
            <div className="space-y-4">
              {data?.metrics && <KpiRow metrics={data.metrics} />}
              {state.panels.length === 0 ? (
                <div className="rounded-xl border border-dashed border-gray-300 bg-gray-50 py-12 text-center">
                  <p className="text-sm text-gray-500">No panels. Add one or explore metrics.</p>
                </div>
              ) : (
                <PanelGrid
                  panels={state.panels}
                  ctx={ctx}
                  editable={editable}
                  onLayoutChange={onLayoutChange}
                  onEdit={openEdit}
                  onRemove={removePanel}
                />
              )}
            </div>
          )}
        </TabsContent>

        <TabsContent value="explore">
          {data?.metrics ? (
            <MetricsExplorer metrics={data.metrics} onAdd={quickAdd} />
          ) : (
            <div className="flex h-48 items-center justify-center">
              <Spinner size="lg" />
            </div>
          )}
        </TabsContent>
      </Tabs>

      <AddPanelDialog
        open={dialogOpen}
        onClose={() => setDialogOpen(false)}
        onSave={addOrSavePanel}
        editing={editingPanel}
        availableMetrics={metricNames}
        labelKeys={Object.keys(labels.labels).sort()}
      />
    </div>
  );
}

function EmptyState() {
  return (
    <div className="flex h-48 flex-col items-center justify-center gap-1 rounded-xl border border-dashed border-gray-300 bg-gray-50 text-center">
      <p className="text-sm font-medium text-gray-500">No metrics in this range</p>
      <p className="text-xs text-gray-400">Charts appear automatically once this resource reports metrics.</p>
    </div>
  );
}

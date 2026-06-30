"use client";
import { useMemo } from "react";
import * as ReactGridLayout from "react-grid-layout";
import "react-grid-layout/css/styles.css";
import "react-resizable/css/styles.css";
import { Panel, type PanelContext } from "./panel";
import type { PanelConfig } from "./dashboard-types";

export interface GridLayoutItem {
  i: string;
  x: number;
  y: number;
  w: number;
  h: number;
}

const ResponsiveGrid = ReactGridLayout.WidthProvider(ReactGridLayout.Responsive);

const COLS = { lg: 12, md: 12, sm: 6, xs: 2 };
const ROW_HEIGHT = 40;

/**
 * PanelGrid renders the dashboard's panels on a 12-column react-grid-layout.
 * Drag (via the panel header handle) and resize are enabled only in edit mode;
 * layout changes flow back to the persisted dashboard state.
 */
export function PanelGrid({
  panels,
  ctx,
  editable,
  onLayoutChange,
  onEdit,
  onRemove,
}: {
  panels: PanelConfig[];
  ctx: PanelContext;
  editable: boolean;
  onLayoutChange: (layout: GridLayoutItem[]) => void;
  onEdit: (p: PanelConfig) => void;
  onRemove: (id: string) => void;
}) {
  const layout: GridLayoutItem[] = useMemo(
    () =>
      panels.map((p) => ({
        i: p.id,
        x: p.x,
        y: p.y,
        w: p.w,
        h: p.h,
        minW: 2,
        minH: 4,
      })),
    [panels],
  );

  return (
    <ResponsiveGrid
      className="layout"
      layouts={{ lg: layout, md: layout }}
      breakpoints={{ lg: 1200, md: 900, sm: 640, xs: 0 }}
      cols={COLS}
      rowHeight={ROW_HEIGHT}
      margin={[16, 16]}
      containerPadding={[0, 0]}
      draggableHandle=".panel-drag"
      isDraggable={editable}
      isResizable={editable}
      onLayoutChange={(l) => onLayoutChange(l)}
      compactType="vertical"
      resizeHandles={["se"]}
    >
      {panels.map((p) => (
        <div key={p.id}>
          <Panel panel={p} ctx={ctx} editable={editable} onEdit={onEdit} onRemove={onRemove} />
        </div>
      ))}
    </ResponsiveGrid>
  );
}

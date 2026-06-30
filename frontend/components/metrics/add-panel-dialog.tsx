"use client";
import { useEffect, useState } from "react";
import { Modal } from "@/components/ui/modal";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "@/components/ui/select";
import { VIZ_LABELS, type VizType } from "@/components/charts/types";
import { AGG_OPTIONS, type PanelConfig } from "./dashboard-types";
import { cn } from "@/lib/cn";

const INHERIT = "__inherit";

/**
 * AddPanelDialog is the panel editor MVP: pick a metric, a visualization, and
 * optional per-panel group-by / aggregation overrides. Editing an existing panel
 * pre-fills the form; the parent owns placement on the grid.
 */
export function AddPanelDialog({
  open,
  onClose,
  onSave,
  editing,
  availableMetrics,
  labelKeys,
}: {
  open: boolean;
  onClose: () => void;
  onSave: (p: Omit<PanelConfig, "id" | "x" | "y" | "w" | "h"> & Partial<Pick<PanelConfig, "id">>) => void;
  editing: PanelConfig | null;
  availableMetrics: string[];
  labelKeys: string[];
}) {
  const [metric, setMetric] = useState("");
  const [viz, setViz] = useState<VizType>("line");
  const [title, setTitle] = useState("");
  const [groupBy, setGroupBy] = useState<string>(INHERIT);
  const [agg, setAgg] = useState<string>(INHERIT);

  useEffect(() => {
    if (!open) return;
    // Sync the form to props each time the dialog opens — legit external sync.
    /* eslint-disable react-hooks/set-state-in-effect */
    if (editing) {
      setMetric(editing.metric);
      setViz(editing.viz);
      setTitle(editing.title ?? "");
      setGroupBy(editing.groupBy === undefined ? INHERIT : editing.groupBy || "__none");
      setAgg(editing.agg === undefined ? INHERIT : editing.agg || "");
    } else {
      setMetric(availableMetrics[0] ?? "");
      setViz("line");
      setTitle("");
      setGroupBy(INHERIT);
      setAgg(INHERIT);
    }
    /* eslint-enable react-hooks/set-state-in-effect */
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, editing]);

  function submit() {
    if (!metric) return;
    onSave({
      id: editing?.id,
      metric,
      viz,
      title: title.trim() || undefined,
      groupBy: groupBy === INHERIT ? undefined : groupBy === "__none" ? "" : groupBy,
      agg: agg === INHERIT ? undefined : agg,
    });
    onClose();
  }

  const vizKeys = Object.keys(VIZ_LABELS) as VizType[];

  return (
    <Modal isOpen={open} onClose={onClose} title={editing ? "Edit panel" : "Add panel"}>
      <div className="space-y-4">
        <Field label="Metric">
          <Select value={metric} onValueChange={setMetric}>
            <SelectTrigger className="w-full">
              <SelectValue placeholder="Select a metric" />
            </SelectTrigger>
            <SelectContent>
              {availableMetrics.length === 0 && (
                <SelectItem value="__empty" disabled>
                  No metrics discovered yet
                </SelectItem>
              )}
              {availableMetrics.map((m) => (
                <SelectItem key={m} value={m}>
                  {m}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>

        <Field label="Visualization">
          <div className="grid grid-cols-3 gap-1.5">
            {vizKeys.map((v) => (
              <button
                key={v}
                onClick={() => setViz(v)}
                className={cn(
                  "rounded-lg border px-2 py-1.5 text-xs font-medium transition-colors",
                  viz === v
                    ? "border-blue-600 bg-blue-50 text-blue-700"
                    : "border-gray-200 bg-white text-gray-600 hover:border-gray-300",
                )}
              >
                {VIZ_LABELS[v]}
              </button>
            ))}
          </div>
        </Field>

        <Field label="Title (optional)">
          <input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder={metric}
            className="block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </Field>

        <div className="grid grid-cols-2 gap-3">
          <Field label="Group by (override)">
            <Select value={groupBy} onValueChange={setGroupBy}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={INHERIT}>Inherit</SelectItem>
                <SelectItem value="__none">None</SelectItem>
                {labelKeys.map((k) => (
                  <SelectItem key={k} value={k}>
                    by {k}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field label="Aggregation (override)">
            <Select value={agg} onValueChange={setAgg}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={INHERIT}>Inherit</SelectItem>
                {AGG_OPTIONS.filter((o) => o.value !== "").map((o) => (
                  <SelectItem key={o.value} value={o.value}>
                    {o.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </div>

        <div className="flex justify-end gap-2 pt-1">
          <button
            onClick={onClose}
            className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100"
          >
            Cancel
          </button>
          <button
            onClick={submit}
            disabled={!metric}
            className="rounded-lg bg-gray-900 px-4 py-2 text-sm font-medium text-white hover:bg-gray-800 disabled:opacity-50"
          >
            {editing ? "Save panel" : "Add panel"}
          </button>
        </div>
      </div>
    </Modal>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="mb-1 block text-xs font-semibold uppercase tracking-wide text-gray-500">{label}</label>
      {children}
    </div>
  );
}

"use client";
import { useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { Modal } from "@/components/ui/modal";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "@/components/ui/select";
import { VIZ_LABELS, type VizType, type Threshold, type Annotation } from "@/components/charts/types";
import { AGG_OPTIONS, type PanelConfig } from "./dashboard-types";
import { cn } from "@/lib/cn";

const INHERIT = "__inherit";

/** Palette offered for new threshold / annotation markers. */
const MARKER_COLORS = ["#dc2626", "#ea580c", "#ca8a04", "#059669", "#2563eb", "#7c3aed"];

/** datetime-local string (local tz) ↔ unix seconds helpers for annotations. */
function toLocalInput(unixSec: number): string {
  const d = new Date(unixSec * 1000);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
function fromLocalInput(s: string): number {
  const ms = new Date(s).getTime();
  return Number.isNaN(ms) ? 0 : Math.floor(ms / 1000);
}

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
  const [thresholds, setThresholds] = useState<Threshold[]>([]);
  const [annotations, setAnnotations] = useState<Annotation[]>([]);

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
      setThresholds(editing.thresholds ?? []);
      setAnnotations(editing.annotations ?? []);
    } else {
      setMetric(availableMetrics[0] ?? "");
      setViz("line");
      setTitle("");
      setGroupBy(INHERIT);
      setAgg(INHERIT);
      setThresholds([]);
      setAnnotations([]);
    }
    /* eslint-enable react-hooks/set-state-in-effect */
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, editing]);

  function submit() {
    if (!metric) return;
    const cleanThresholds = thresholds.filter((t) => Number.isFinite(t.value));
    const cleanAnnotations = annotations.filter((a) => a.time > 0 && a.label.trim() !== "");
    onSave({
      id: editing?.id,
      metric,
      viz,
      title: title.trim() || undefined,
      groupBy: groupBy === INHERIT ? undefined : groupBy === "__none" ? "" : groupBy,
      agg: agg === INHERIT ? undefined : agg,
      thresholds: cleanThresholds.length ? cleanThresholds : undefined,
      annotations: cleanAnnotations.length ? cleanAnnotations : undefined,
    });
    onClose();
  }

  const isTimeline = viz === "status-timeline";

  function addThreshold() {
    setThresholds((t) => [
      ...t,
      { value: 0, color: MARKER_COLORS[t.length % MARKER_COLORS.length], label: "" },
    ]);
  }
  function patchThreshold(i: number, patch: Partial<Threshold>) {
    setThresholds((t) => t.map((x, j) => (j === i ? { ...x, ...patch } : x)));
  }
  function removeThreshold(i: number) {
    setThresholds((t) => t.filter((_, j) => j !== i));
  }

  function addAnnotation() {
    setAnnotations((a) => [
      ...a,
      { time: Math.floor(Date.now() / 1000), label: "", color: MARKER_COLORS[a.length % MARKER_COLORS.length] },
    ]);
  }
  function patchAnnotation(i: number, patch: Partial<Annotation>) {
    setAnnotations((a) => a.map((x, j) => (j === i ? { ...x, ...patch } : x)));
  }
  function removeAnnotation(i: number) {
    setAnnotations((a) => a.filter((_, j) => j !== i));
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
                    ? "border-blue-600 bg-blue-50 dark:bg-blue-950/40 text-blue-700 dark:text-blue-300"
                    : "border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 text-gray-600 dark:text-gray-400 hover:border-gray-300 dark:hover:border-gray-700",
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
            className="block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 dark:bg-gray-800 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
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

        <Field
          label={isTimeline ? "Status bands (by threshold)" : "Thresholds"}
        >
          <p className="-mt-0.5 mb-1.5 text-[11px] text-gray-400 dark:text-gray-500">
            {isTimeline
              ? "Each band colors samples at or above its value; the highest matching wins."
              : "Horizontal reference lines drawn across the chart."}
          </p>
          <div className="space-y-1.5">
            {thresholds.map((t, i) => (
              <div key={i} className="flex items-center gap-1.5">
                <input
                  type="number"
                  value={Number.isFinite(t.value) ? t.value : ""}
                  onChange={(e) => patchThreshold(i, { value: parseFloat(e.target.value) })}
                  placeholder="value"
                  className="w-24 rounded-md border border-gray-300 dark:border-gray-700 px-2 py-1.5 text-xs text-gray-900 dark:text-gray-100 dark:bg-gray-800 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
                <ColorPicker value={t.color} onChange={(c) => patchThreshold(i, { color: c })} />
                <input
                  value={t.label ?? ""}
                  onChange={(e) => patchThreshold(i, { label: e.target.value })}
                  placeholder="label (optional)"
                  className="min-w-0 flex-1 rounded-md border border-gray-300 dark:border-gray-700 px-2 py-1.5 text-xs text-gray-900 dark:text-gray-100 dark:bg-gray-800 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
                <RemoveBtn onClick={() => removeThreshold(i)} />
              </div>
            ))}
            <AddRowBtn label={isTimeline ? "Add band" : "Add threshold"} onClick={addThreshold} />
          </div>
        </Field>

        {!isTimeline && (
          <Field label="Annotations">
            <p className="-mt-0.5 mb-1.5 text-[11px] text-gray-400 dark:text-gray-500">
              Vertical markers at a fixed time (e.g. a deploy or incident).
            </p>
            <div className="space-y-1.5">
              {annotations.map((a, i) => (
                <div key={i} className="flex items-center gap-1.5">
                  <input
                    type="datetime-local"
                    value={a.time ? toLocalInput(a.time) : ""}
                    onChange={(e) => patchAnnotation(i, { time: fromLocalInput(e.target.value) })}
                    className="rounded-md border border-gray-300 dark:border-gray-700 px-2 py-1.5 text-xs text-gray-900 dark:text-gray-100 dark:bg-gray-800 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                  />
                  <ColorPicker value={a.color ?? MARKER_COLORS[0]} onChange={(c) => patchAnnotation(i, { color: c })} />
                  <input
                    value={a.label}
                    onChange={(e) => patchAnnotation(i, { label: e.target.value })}
                    placeholder="label"
                    className="min-w-0 flex-1 rounded-md border border-gray-300 dark:border-gray-700 px-2 py-1.5 text-xs text-gray-900 dark:text-gray-100 dark:bg-gray-800 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                  />
                  <RemoveBtn onClick={() => removeAnnotation(i)} />
                </div>
              ))}
              <AddRowBtn label="Add annotation" onClick={addAnnotation} />
            </div>
          </Field>
        )}

        <div className="flex justify-end gap-2 pt-1">
          <button
            onClick={onClose}
            className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800"
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
      <label className="mb-1 block text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{label}</label>
      {children}
    </div>
  );
}

function ColorPicker({ value, onChange }: { value: string; onChange: (c: string) => void }) {
  return (
    <input
      type="color"
      value={value}
      onChange={(e) => onChange(e.target.value)}
      title="Marker color"
      className="h-8 w-8 shrink-0 cursor-pointer rounded-md border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 p-0.5"
    />
  );
}

function RemoveBtn({ onClick }: { onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      title="Remove"
      className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-gray-400 dark:text-gray-500 hover:bg-red-50 dark:hover:bg-red-950/40 hover:text-red-600 dark:hover:text-red-400"
    >
      <Trash2 className="h-3.5 w-3.5" />
    </button>
  );
}

function AddRowBtn({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-blue-600 dark:text-blue-400 hover:bg-blue-50 dark:hover:bg-blue-950/40"
    >
      <Plus className="h-3.5 w-3.5" /> {label}
    </button>
  );
}

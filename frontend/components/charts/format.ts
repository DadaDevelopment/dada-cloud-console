/**
 * Value formatting shared by every chart and KPI. Units come from the backend
 * metric spec ("%", "B", "B/s", "cores") or are inferred from the metric name;
 * unknown units fall back to a compact SI-suffixed number.
 */
export function formatValue(v: number, unit = ""): string {
  if (!isFinite(v)) return "—";
  if (unit === "%") return `${v.toFixed(1)}%`;
  if (unit === "cores") return v < 1 ? `${(v * 1000).toFixed(0)}m` : `${v.toFixed(2)}`;
  if (unit === "B" || unit === "B/s") return formatBytes(v, unit === "B/s");
  if (unit === "s" || unit === "ms") return formatDuration(v, unit);
  return formatCompact(v) + (unit ? ` ${unit}` : "");
}

export function formatBytes(v: number, perSec = false): string {
  const suffix = perSec ? "/s" : "";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let n = Math.abs(v);
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  const sign = v < 0 ? "-" : "";
  return `${sign}${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}${suffix}`;
}

export function formatDuration(v: number, unit: "s" | "ms"): string {
  const ms = unit === "ms" ? v : v * 1000;
  if (ms < 1) return `${(ms * 1000).toFixed(0)}µs`;
  if (ms < 1000) return `${ms.toFixed(ms < 10 ? 1 : 0)}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(2)}s`;
  const m = Math.floor(s / 60);
  return `${m}m${Math.round(s % 60)}s`;
}

export function formatCompact(v: number): string {
  const abs = Math.abs(v);
  if (abs >= 1e9) return `${(v / 1e9).toFixed(1)}B`;
  if (abs >= 1e6) return `${(v / 1e6).toFixed(1)}M`;
  if (abs >= 1e3) return `${(v / 1e3).toFixed(1)}k`;
  if (abs >= 1) return v.toFixed(abs < 10 ? 2 : 0);
  if (abs === 0) return "0";
  return v.toFixed(3);
}

/**
 * inferUnit guesses a display unit from a Prometheus-style metric name when the
 * backend ships an empty unit (the common case for custom metrics).
 */
export function inferUnit(name: string): string {
  const n = name.toLowerCase();
  if (n.includes("bytes") || n.endsWith("_bytes")) return "B";
  if (n.includes("percent") || n.endsWith("_pct") || n.endsWith("_ratio")) return "%";
  if (n.includes("seconds") || n.endsWith("_seconds")) return "s";
  if (n.includes("cores")) return "cores";
  return "";
}

/** formatTimeAxis renders a chart-axis timestamp (ms) compactly. */
export function formatTimeAxis(ms: number, wideRange: boolean): string {
  const d = new Date(ms);
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  if (wideRange) {
    return `${String(d.getMonth() + 1).padStart(2, "0")}/${String(d.getDate()).padStart(2, "0")} ${hh}:${mm}`;
  }
  return `${hh}:${mm}`;
}

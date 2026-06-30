/**
 * ECharts theme tokens for the observability dashboard.
 *
 * The console has no class-based dark switch (it follows prefers-color-scheme),
 * so charts resolve their palette from the OS scheme at render time and re-init
 * when it flips. Keep these aligned with the Tailwind grays used by panel chrome.
 */

export type ChartTheme = "light" | "dark";

/**
 * SERIES_PALETTE is the categorical color ramp shared by every multi-series
 * chart so a given series keeps a stable, legible color across panels. Tuned for
 * contrast on both light and dark backgrounds.
 */
export const SERIES_PALETTE = [
  "#3b82f6",
  "#8b5cf6",
  "#f97316",
  "#10b981",
  "#06b6d4",
  "#ec4899",
  "#eab308",
  "#ef4444",
  "#14b8a6",
  "#a855f7",
  "#84cc16",
  "#f43f5e",
];

interface ThemeTokens {
  axisLine: string;
  axisLabel: string;
  splitLine: string;
  tooltipBg: string;
  tooltipBorder: string;
  tooltipText: string;
  text: string;
  muted: string;
}

export const THEME_TOKENS: Record<ChartTheme, ThemeTokens> = {
  light: {
    axisLine: "#e5e7eb",
    axisLabel: "#6b7280",
    splitLine: "#f1f5f9",
    tooltipBg: "rgba(255,255,255,0.98)",
    tooltipBorder: "#e5e7eb",
    tooltipText: "#111827",
    text: "#374151",
    muted: "#9ca3af",
  },
  dark: {
    axisLine: "#27272a",
    axisLabel: "#a1a1aa",
    splitLine: "#1f2937",
    tooltipBg: "rgba(24,24,27,0.98)",
    tooltipBorder: "#3f3f46",
    tooltipText: "#f4f4f5",
    text: "#d4d4d8",
    muted: "#71717a",
  },
};

/**
 * colorForLabel maps an arbitrary series label to a stable palette color by
 * hashing the label, so a group-by value (e.g. "code=500") keeps its color even
 * as series order changes across refreshes.
 */
export function colorForLabel(label: string, index: number): string {
  if (!label) return SERIES_PALETTE[index % SERIES_PALETTE.length];
  let h = 0;
  for (let i = 0; i < label.length; i++) h = (h * 31 + label.charCodeAt(i)) >>> 0;
  return SERIES_PALETTE[h % SERIES_PALETTE.length];
}

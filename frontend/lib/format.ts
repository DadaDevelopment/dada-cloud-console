// Shared formatting helpers (previously duplicated per-page).

/** Compact relative time, e.g. "5m ago", "3h ago", "2d ago". */
export function timeAgo(dateStr: string): string {
  const diffSecs = Math.floor((Date.now() - new Date(dateStr).getTime()) / 1000);
  if (diffSecs < 60) return `${diffSecs}s ago`;
  const diffMins = Math.floor(diffSecs / 60);
  if (diffMins < 60) return `${diffMins}m ago`;
  const diffHours = Math.floor(diffMins / 60);
  if (diffHours < 24) return `${diffHours}h ago`;
  return `${Math.floor(diffHours / 24)}d ago`;
}

/**
 * Formats a ruble amount with a ru-locale thousands separator and no decimals,
 * suffixed with the ₽ sign, e.g. `1234.5` → "1 235 ₽". Fractional kopecks are
 * rounded away because consumption estimates are shown at whole-ruble grain.
 */
export function formatRub(amount: number): string {
  return `${Math.round(amount).toLocaleString("ru-RU")} ₽`;
}

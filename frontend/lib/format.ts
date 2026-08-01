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
 * Compact time remaining, e.g. "in 45m", "in 3h". Returns null once the instant
 * has passed, so a caller renders "expired" in its own words rather than
 * timeAgo's negative counter.
 */
export function timeUntil(dateStr: string): string | null {
  const secs = Math.floor((new Date(dateStr).getTime() - Date.now()) / 1000);
  if (secs <= 0) return null;
  if (secs < 60) return `in ${secs}s`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `in ${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `in ${hours}h`;
  return `in ${Math.floor(hours / 24)}d`;
}

/**
 * Formats a ruble amount with a ru-locale thousands separator and no decimals,
 * suffixed with the ₽ sign, e.g. `1234.5` → "1 235 ₽". Fractional kopecks are
 * rounded away because consumption estimates are shown at whole-ruble grain.
 */
export function formatRub(amount: number): string {
  return `${Math.round(amount).toLocaleString("ru-RU")} ₽`;
}

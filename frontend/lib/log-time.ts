/**
 * Format an Elasticsearch log timestamp in the user's local browser timezone.
 * The optional timezone override keeps the formatter deterministic in tests;
 * the viewer omits it and follows the browser's configured timezone.
 */
export function formatLogTime(timestamp: string, timeZone?: string): string {
  if (!timestamp) return "";

  const date = new Date(timestamp);
  if (Number.isNaN(date.getTime())) return timestamp;

  return new Intl.DateTimeFormat(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
    ...(timeZone ? { timeZone } : {}),
  }).format(date);
}

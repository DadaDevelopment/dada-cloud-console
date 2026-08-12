/**
 * Presentation mapping for a support ticket's lifecycle status, shared by the
 * "my tickets" list in the support modal. Pure and framework-free so it can
 * be unit tested without mounting React.
 */

export type FeedbackAgeUnit = "hours" | "days";

const STATUS_BADGE_CLASS: Record<string, string> = {
  new: "bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-400",
  in_progress: "bg-blue-50 text-blue-700 dark:bg-blue-950/40 dark:text-blue-400",
  resolved: "bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400",
};

const STATUS_LABEL_KEY: Record<string, string> = {
  new: "feedback.mine.status.new",
  in_progress: "feedback.mine.status.inProgress",
  resolved: "feedback.mine.status.resolved",
};

/** Tailwind classes for a ticket's status pill. Unknown statuses read as resolved (neutral, non-alarming). */
export function feedbackStatusBadgeClass(status: string): string {
  return STATUS_BADGE_CLASS[status] ?? STATUS_BADGE_CLASS.resolved;
}

/** i18n message key for a ticket's status label. Unknown statuses fall back to the "resolved" label. */
export function feedbackStatusLabelKey(status: string): string {
  return STATUS_LABEL_KEY[status] ?? STATUS_LABEL_KEY.resolved;
}

/**
 * Splits the age of a ticket into a translatable {unit, count} pair, mirroring
 * the admin feedback queue's hours/days cutover at 48h.
 */
export function feedbackAgeParts(createdAt: string, nowMs: number = Date.now()): { unit: FeedbackAgeUnit; count: number } {
  const created = new Date(createdAt).getTime();
  const hours = Number.isNaN(created) ? 0 : Math.max(0, Math.floor((nowMs - created) / 3_600_000));
  if (hours >= 48) return { unit: "days", count: Math.floor(hours / 24) };
  return { unit: "hours", count: hours };
}

/** First non-empty line of a ticket message, for compact list rendering. */
export function feedbackFirstLine(message: string): string {
  const line = message.split("\n").find((l) => l.trim().length > 0) ?? "";
  return line.trim();
}

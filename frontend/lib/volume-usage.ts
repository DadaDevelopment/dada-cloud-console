export interface VolumeUsage {
  ratio: number;
  inodes_used?: number;
  inodes_total?: number;
  inodes_ratio?: number;
}

export type UsageSeverity = "ok" | "warn" | "crit";

export interface VolumeUsageView {
  bytesRatio: number;
  bytesSeverity: UsageSeverity;
  hasInodes: boolean;
  inodesRatio: number | null;
  inodesSeverity: UsageSeverity;
  overallSeverity: UsageSeverity;
  displayRatio: number;
}

const SEVERITY_RANK: Record<UsageSeverity, number> = { ok: 0, warn: 1, crit: 2 };

function ratioSeverity(ratio: number): UsageSeverity {
  if (ratio >= 0.95) return "crit";
  if (ratio >= 0.85) return "warn";
  return "ok";
}

/**
 * Classifies a volume usage reading into byte and inode severities. Inode
 * fields are optional: the metric source can be blind to them (ext4 df -i
 * unavailable, cloud driver that does not report it), in which case they are
 * treated as absent rather than as zero usage - an absent reading must never
 * render as a calm 0%.
 */
export function evaluateVolumeUsage(usage: VolumeUsage): VolumeUsageView {
  const bytesRatio = usage.ratio;
  const bytesSeverity = ratioSeverity(bytesRatio);
  const hasInodes = typeof usage.inodes_ratio === "number" && typeof usage.inodes_total === "number";
  const inodesRatio = hasInodes ? (usage.inodes_ratio as number) : null;
  const inodesSeverity = hasInodes ? ratioSeverity(inodesRatio as number) : "ok";
  const overallSeverity =
    SEVERITY_RANK[inodesSeverity] > SEVERITY_RANK[bytesSeverity] ? inodesSeverity : bytesSeverity;
  const displayRatio = hasInodes ? Math.max(bytesRatio, inodesRatio as number) : bytesRatio;
  return { bytesRatio, bytesSeverity, hasInodes, inodesRatio, inodesSeverity, overallSeverity, displayRatio };
}

export function severityTextClass(severity: UsageSeverity): string {
  if (severity === "crit") return "font-medium text-red-600 dark:text-red-400";
  if (severity === "warn") return "font-medium text-amber-600 dark:text-amber-500";
  return "font-medium text-gray-700 dark:text-gray-300";
}

export function severityBarClass(severity: UsageSeverity): string {
  if (severity === "crit") return "h-full rounded-full bg-red-600";
  if (severity === "warn") return "h-full rounded-full bg-amber-500";
  return "h-full rounded-full bg-blue-600";
}

/**
 * Formats an inode/file count with plain-ASCII thousands separators (a
 * regular space, never U+00A0 - Intl's ru-RU grouping uses the non-breaking
 * space, which is banned house-wide). Negative and non-finite input renders
 * as an em dash rather than a misleading number.
 */
export function formatCount(n: number): string {
  if (!isFinite(n) || n < 0) return "-";
  const rounded = Math.round(n);
  return rounded.toString().replace(/\B(?=(\d{3})+(?!\d))/g, " ");
}

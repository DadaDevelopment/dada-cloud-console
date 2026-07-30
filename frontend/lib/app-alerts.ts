import type { ResourceSnapshot } from "@/lib/types";

export type AppAlertType = "crash" | "volume";

export interface AppAlert {
  type: AppAlertType;
  reason?: string;
  detail?: string;
  ratio?: number;
  detected_at: string;
}

/**
 * Reads the optional `alerts` field off a resource snapshot. The backend adds
 * this field only once the write-back has shipped; older backend builds and
 * cached snapshots simply omit it, so this always returns an array (empty
 * when the field is missing) rather than throwing on stale data.
 */
export function getAppAlerts(snapshot: ResourceSnapshot | null | undefined): AppAlert[] {
  if (!snapshot) return [];
  const raw = (snapshot as unknown as { alerts?: unknown }).alerts;
  if (!Array.isArray(raw)) return [];
  return raw.filter(
    (a): a is AppAlert =>
      !!a &&
      typeof a === "object" &&
      ((a as AppAlert).type === "crash" || (a as AppAlert).type === "volume") &&
      typeof (a as AppAlert).detected_at === "string",
  );
}

/** True if any alert in the list is the given type. */
export function hasAlertType(alerts: AppAlert[], type: AppAlertType): boolean {
  return alerts.some((a) => a.type === type);
}

import type { ResourceSnapshot } from "@/lib/types";

export type AppAlertType = "crash" | "volume" | "url";

/**
 * `cause` / `cause_line` are the crash explanation the watcher already worked
 * out at detection time (see backend notify.ClassifyCrashLog /
 * ExtractCauseLine). `cause` is prose written by the backend in Russian and is
 * therefore NOT rendered directly — it is only a flag that the platform
 * recognised a crash cause, so the console can show its own localised
 * wording keyed off `cause_kind`. `cause_line` is the raw matched log line:
 * language neutral, and the single most useful thing to put in front of the
 * owner. Both are absent whenever the log read failed or nothing matched.
 *
 * `cause_kind` tells the console WHO is at fault: `"app_code"` means the
 * app's own code, `"platform_network"` / `"platform_storage"` mean the
 * platform side broke (network route or disk/volume) and the user's code is
 * not to blame. Missing or empty means the backend could not classify it —
 * the console must not guess "your code" in that case.
 */
export type AppAlertCauseKind = "app_code" | "platform_network" | "platform_storage";

export interface AppAlert {
  type: AppAlertType;
  reason?: string;
  detail?: string;
  cause?: string;
  cause_line?: string;
  cause_kind?: AppAlertCauseKind;
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
      ((a as AppAlert).type === "crash" ||
        (a as AppAlert).type === "volume" ||
        (a as AppAlert).type === "url") &&
      typeof (a as AppAlert).detected_at === "string",
  );
}

export function getOperationalAppAlerts(alerts: AppAlert[]): AppAlert[] {
	return alerts;
}

/** True if any alert in the list is the given type. */
export function hasAlertType(alerts: AppAlert[], type: AppAlertType): boolean {
  return alerts.some((a) => a.type === type);
}

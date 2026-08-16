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
 * app's own code, `"platform_network"` / `"platform_storage"` /
 * `"platform_registry"` mean the platform side broke (network route,
 * disk/volume, or image delivery to the registry) and the user's code is
 * not to blame, `"resource_limit"` means the container was stopped for
 * exceeding its plan's memory ceiling — neither a platform bug nor a code
 * bug, just a capacity fact, so it must render as neutral as the platform
 * kinds and never as an accusation. `"app_needs_args"` means the program
 * refused an empty command line (a CLI tool started as a service) — also
 * neutral: nothing is broken, the app's shape and the way we start it simply
 * do not match. `"bad_connection_string"` means a connection-string-shaped
 * env var (DATABASE_URL, REDIS_URL, ...) holds a bare host with no scheme —
 * also neutral (not the owner's code, not a platform bug, just a value that
 * needs the full string). For this one kind ONLY, `cause_line` does not hold
 * a raw log line: it holds "KEY=VALUE" naming the exact env var and its
 * current bad value, because the crash log itself cannot be trusted here
 * (Node's pg-connection-string reports a phantom host "base" instead of the
 * real value — see the backend's notify.ClassifyConnectionStringFailure doc
 * comment). `"ssl_not_supported"` means a connection-string-shaped env var
 * already has a scheme and holds a well-formed DSN, but the client was told
 * to negotiate SSL against a database endpoint that does not support it
 * (see the backend's notify.ClassifySSLNotSupported) — also neutral. Here
 * `cause_line` holds ONLY the bare key name, never "KEY=VALUE": unlike the
 * bare-host case this DSN can carry a real password, so the backend never
 * writes the value into cause/cause_line at all — the console reveals the
 * current value itself, on demand, right before writing the fix back (see
 * AppAlertsBanner's SSL repair block). `"missing_env_var"` means the app
 * asked for an environment variable that is not set on it (see the backend's
 * notify.ClassifyMissingEnvVar) — neutral again: the code is fine, the
 * platform is fine, one input is absent. `cause_line` holds ONLY the bare key
 * name, and there is no value to show by construction. Missing or empty means
 * the backend could not classify it — the console must not guess "your code"
 * in that case.
 */
export type AppAlertCauseKind =
  | "app_code"
  | "platform_network"
  | "platform_storage"
  | "platform_registry"
  | "resource_limit"
  | "app_needs_args"
  | "bad_connection_string"
  | "ssl_not_supported"
  | "missing_env_var";

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

/**
 * Splits a `bad_connection_string` alert's `cause_line` ("KEY=VALUE") back
 * into the env var key and its current bad value. See AppAlertCauseKind's
 * doc comment above for why this one cause kind repurposes cause_line
 * instead of a raw log line. Split on the FIRST "=" only: the value side is
 * always a bare host by construction (the backend classifier only ever
 * matches scheme-less values), which cannot itself contain "=", but keeping
 * the split conservative costs nothing. Returns null for a missing or
 * malformed line so callers can fall back to showing no repair affordance
 * rather than guessing.
 */
export function parseBadConnCauseLine(causeLine?: string): { key: string; value: string } | null {
  if (!causeLine) return null;
  const idx = causeLine.indexOf("=");
  if (idx <= 0) return null;
  return { key: causeLine.slice(0, idx), value: causeLine.slice(idx + 1) };
}

/**
 * True when dsn's query string already sets an "sslmode" parameter
 * (case-insensitively). Mirrors the backend's notify.hasSSLModeParam
 * exactly — see connstring_cause.go's doc comment on connStringHasSchemePrefix
 * for why this codebase duplicates small parsing helpers between the two
 * packages rather than sharing them across the Go/TypeScript boundary.
 */
function hasSSLModeParam(dsn: string): boolean {
  const idx = dsn.indexOf("?");
  if (idx === -1) return false;
  return dsn
    .slice(idx + 1)
    .split("&")
    .some((pair) => {
      const eq = pair.indexOf("=");
      const key = eq >= 0 ? pair.slice(0, eq) : pair;
      return key.toLowerCase() === "sslmode";
    });
}

/**
 * Returns dsn with "sslmode=disable" added to its query string, preserving
 * every existing parameter and never duplicating the key if already
 * present. Mirrors the backend's notify.SuggestSSLModeDisable exactly (see
 * that function's doc comment for why the fix is "add one parameter", not
 * "replace the whole value" — the DSN is otherwise well-formed).
 */
export function suggestSSLModeDisable(dsn: string): string {
  if (hasSSLModeParam(dsn)) return dsn;
  if (!dsn.includes("?")) return `${dsn}?sslmode=disable`;
  if (dsn.endsWith("?") || dsn.endsWith("&")) return `${dsn}sslmode=disable`;
  return `${dsn}&sslmode=disable`;
}

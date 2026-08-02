/** Placeholder printed instead of a secret-bearing value. */
export const REDACTED = "[redacted]";

const EXACT_SECRET_KEYS = new Set([
  "value",
  "values",
  "env",
  "envs",
  "environment",
  "secret",
  "secrets",
  "token",
  "password",
  "passwd",
  "pass",
  "credential",
  "credentials",
  "auth",
  "authorization",
  "cookie",
  "dsn",
  "connectionstring",
  "connectionuri",
  "privatekey",
  "publickey",
  "apikey",
  "accesskey",
  "secretkey",
  "sshprivatekey",
  "certificate",
  "cert",
  "webhooksecret",
]);

const SECRET_KEY_FRAGMENTS = [
  "token",
  "secret",
  "password",
  "passwd",
  "passphrase",
  "apikey",
  "privatekey",
  "accesskey",
  "credential",
  "authorization",
];

function normalizeKey(key: string): string {
  return key.toLowerCase().replace(/[^a-z0-9]/g, "");
}

/**
 * True when a tool argument named key is expected to carry a secret.
 *
 * Deliberately name-based and conservative in one direction only: a plain
 * `key` stays visible, because in the `{key, value}` shape used by env-var
 * tools it holds the variable name (already spelled out in the confirmation
 * summary) while the secret lives in `value`. Anything whose name contains a
 * secret-ish fragment is redacted whatever its nesting.
 */
export function isSecretArgKey(key: string): boolean {
  const normalized = normalizeKey(key);
  if (!normalized) return false;
  if (EXACT_SECRET_KEYS.has(normalized)) return true;
  return SECRET_KEY_FRAGMENTS.some((fragment) => normalized.includes(fragment));
}

/**
 * Recursively replaces secret-bearing values inside an already decoded tool
 * argument tree, walking objects and arrays at any depth so a bulkSetEnvVars
 * payload (`vars[].value`) is covered exactly like a flat setEnvVar one.
 *
 * The backend redacts before persisting a trace, but the confirmation card
 * renders whatever arrived over the wire - replayed history, an older backend,
 * any future producer - so the panel redacts again on its own.
 */
export function redactArgValues(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map((item) => redactArgValues(item));
  }
  if (value !== null && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      out[k] = isSecretArgKey(k) ? REDACTED : redactArgValues(v);
    }
    return out;
  }
  return value;
}

/** Renders one redacted argument value as the single line the card prints. */
export function formatArgValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (value === null || value === undefined) return "";
  return JSON.stringify(value);
}

const SUMMARY_DEDUPE_MIN_LENGTH = 8;

/**
 * Builds the `key: value` lines of a confirmation card from raw tool
 * arguments: secrets are replaced, and a long value the summary sentence
 * already states verbatim is dropped so the card does not repeat itself.
 *
 * Dedupe has a length floor because short identifiers - an app name, a project
 * id - collide with ordinary summary prose and are exactly the fields a user
 * checks before approving.
 */
export function confirmArgEntries(
  args: Record<string, unknown> | null | undefined,
  summary?: string,
): Array<[string, string]> {
  if (!args) return [];
  const haystack = (summary ?? "").toLowerCase();
  const entries: Array<[string, string]> = [];
  for (const [key, raw] of Object.entries(args)) {
    const shown = formatArgValue(isSecretArgKey(key) ? REDACTED : redactArgValues(raw));
    if (shown === "") continue;
    const duplicate =
      haystack.length > 0 &&
      shown !== REDACTED &&
      shown.length >= SUMMARY_DEDUPE_MIN_LENGTH &&
      haystack.includes(shown.toLowerCase());
    if (duplicate) continue;
    entries.push([key, shown]);
  }
  return entries;
}

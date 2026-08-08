/**
 * Strips the machine-readable code the build agent prefixes onto a failed
 * build's `error_message`.
 *
 * The agent persists `"<fail_reason>: <detail>"` so the code survives even
 * where only the message column is read. The console already renders that code
 * as a translated sentence above the detail, so leaving the prefix in place
 * shows the reader the same fact twice, once in a language they did not ask
 * for.
 *
 * @param failReason - the build's `fail_reason`, if the API returned one
 * @param errorMessage - the build's raw `error_message`
 * @returns the detail alone, or the message unchanged when it carries no prefix
 */
export function buildFailureDetail(failReason?: string | null, errorMessage?: string | null): string {
  const message = (errorMessage ?? "").trim();
  if (!message || !failReason) return message;
  const prefix = `${failReason}: `;
  return message.startsWith(prefix) ? message.slice(prefix.length).trim() : message;
}

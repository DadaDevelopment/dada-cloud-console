/**
 * Replaces the password inside a `scheme://user:password@host` connection
 * string with a fixed-width placeholder, so a DSN can be shown on screen
 * before the user asks to reveal it. Returns the input unchanged when it
 * carries no credentials.
 */
export function maskDsnPassword(dsn: string): string {
  return dsn.replace(/(:\/\/[^:@/]*:)[^@/]*(@)/, "$1••••••••$2");
}

/**
 * Console locale primitives. Unlike the marketing site (where the URL "/en"
 * prefix is the source of truth so pages stay crawlable), the console is an
 * authenticated app with dozens of nested routes — a URL prefix there buys
 * nothing and complicates every link. So console language is a per-user
 * preference persisted in a cookie and applied client-side.
 */

export type ConsoleLocale = "ru" | "en";

export const CONSOLE_LOCALES: ConsoleLocale[] = ["ru", "en"];
export const DEFAULT_CONSOLE_LOCALE: ConsoleLocale = "ru";
export const CONSOLE_LOCALE_COOKIE = "dada_console_lang";

/** One year, in seconds — the preference should outlive a session. */
const COOKIE_MAX_AGE = 60 * 60 * 24 * 365;

export function isConsoleLocale(value: string | null | undefined): value is ConsoleLocale {
  return value === "ru" || value === "en";
}

/** Reads the persisted locale from document.cookie. Returns null on the server. */
export function readConsoleLocaleCookie(): ConsoleLocale | null {
  if (typeof document === "undefined") return null;
  const match = document.cookie.match(/(?:^|;\s*)dada_console_lang=(ru|en)/);
  return match && isConsoleLocale(match[1]) ? match[1] : null;
}

/** Persists the locale so SSR (via middleware) and future visits agree. */
export function writeConsoleLocaleCookie(locale: ConsoleLocale): void {
  if (typeof document === "undefined") return;
  document.cookie = `${CONSOLE_LOCALE_COOKIE}=${locale};path=/;max-age=${COOKIE_MAX_AGE};samesite=lax`;
}

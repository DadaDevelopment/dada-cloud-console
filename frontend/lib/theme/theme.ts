/**
 * Console theme primitives. Like the console locale, the theme is a per-user
 * preference persisted in a cookie and applied client-side via a `.dark` class
 * on <html> (Tailwind v4 class strategy). Separate from the marketing site,
 * which stays light. On first visit with no stored preference we follow the OS.
 */

export type Theme = "light" | "dark";

export const CONSOLE_THEME_COOKIE = "dada_console_theme";

/** One year, in seconds — the preference should outlive a session. */
const COOKIE_MAX_AGE = 60 * 60 * 24 * 365;

export function isTheme(value: string | null | undefined): value is Theme {
  return value === "light" || value === "dark";
}

/** Reads the persisted theme from document.cookie. Returns null on the server. */
export function readThemeCookie(): Theme | null {
  if (typeof document === "undefined") return null;
  const match = document.cookie.match(/(?:^|;\s*)dada_console_theme=(light|dark)/);
  return match && isTheme(match[1]) ? match[1] : null;
}

/** Persists the theme so future visits agree. */
export function writeThemeCookie(theme: Theme): void {
  if (typeof document === "undefined") return;
  document.cookie = `${CONSOLE_THEME_COOKIE}=${theme};path=/;max-age=${COOKIE_MAX_AGE};samesite=lax`;
}

/** OS color-scheme preference, defaulting to light when unknown (SSR). */
export function osTheme(): Theme {
  if (typeof window === "undefined" || !window.matchMedia) return "light";
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

/** The theme to apply on load: stored preference wins, else the OS default. */
export function resolveInitialTheme(): Theme {
  return readThemeCookie() ?? osTheme();
}

/** Toggles the `.dark` class on <html> to match the active theme. */
export function applyThemeClass(theme: Theme): void {
  if (typeof document === "undefined") return;
  document.documentElement.classList.toggle("dark", theme === "dark");
}

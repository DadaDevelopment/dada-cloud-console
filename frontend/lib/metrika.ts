/**
 * Yandex.Metrika goal helper.
 *
 * Landing CTAs carry a `utm_source` in their href, but the utm alone is not
 * enough to attribute a page: it is dropped at registration and never lands in
 * the database. Firing a goal on click is what makes per-landing conversion
 * visible in Metrika reports.
 */
const YM_ID = Number(process.env.NEXT_PUBLIC_YM_ID ?? "110158915");

type MetrikaFn = (id: number, action: string, goal?: string, params?: Record<string, string>) => void;

/** Reads the `utm_source` value out of a CTA href, or "direct" when absent. */
export function ctaSource(href: string): string {
  const match = /[?&]utm_source=([^&]+)/.exec(href);
  return match ? decodeURIComponent(match[1]) : "direct";
}

/**
 * Sends a Metrika goal. No-op when the counter has not loaded (ad blockers,
 * SSR, local dev) so a missing counter can never break a CTA click.
 */
export function reachGoal(goal: string, params?: Record<string, string>) {
  if (typeof window === "undefined") return;
  const ym = (window as unknown as { ym?: MetrikaFn }).ym;
  if (typeof ym !== "function") return;
  try {
    ym(YM_ID, "reachGoal", goal, params);
  } catch {
    return;
  }
}

/** Goal fired when a visitor clicks the register CTA on a marketing landing. */
export const GOAL_LANDING_CTA = "landing_cta_click";

/** Goal fired on `/register`, just before the visitor is handed to Keycloak. */
export const GOAL_SIGNUP_STARTED = "signup_started";

/** Name of the first-party cookie carrying the acquisition source. */
export const SOURCE_COOKIE = "dada_src";

/**
 * Persists the acquisition source in a first-party cookie scoped to the whole
 * `dada-tuda.ru` zone. The utm only lives in the `/register` query string and is
 * lost the moment Keycloak takes over the redirect, so it has to be written down
 * before that happens if signups are ever to be attributed to a landing page.
 *
 * The first source wins: a returning visitor keeps the page that brought them in.
 */
export function rememberSource(source: string) {
  if (typeof document === "undefined") return;
  if (!source || source === "direct") return;
  if (document.cookie.split("; ").some((c) => c.startsWith(`${SOURCE_COOKIE}=`))) return;
  const safe = encodeURIComponent(source.slice(0, 64));
  const maxAge = 60 * 60 * 24 * 90;
  const domain = document.location.hostname.endsWith("dada-tuda.ru") ? "; domain=.dada-tuda.ru" : "";
  document.cookie = `${SOURCE_COOKIE}=${safe}; path=/; max-age=${maxAge}; samesite=lax${domain}`;
}

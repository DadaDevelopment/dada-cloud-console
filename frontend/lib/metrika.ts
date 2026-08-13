/**
 * Yandex.Metrika goal helper.
 *
 * Landing CTAs carry a `utm_source` in their href, but the utm alone is not
 * enough to attribute a page: it is dropped at registration and never lands in
 * the database. Firing a goal on click is what makes per-landing conversion
 * visible in Metrika reports.
 *
 * Every goal is ALSO mirrored into our own `ux_events` (lib/ux-telemetry.ts).
 * Metrika is anonymous, sampled and blocked for a large share of the audience,
 * and its data never joins a `users` row — so a conversion that lives only
 * there cannot be put on the same timeline as the audit action it produced.
 * The mirror is what makes visit → goal → audit action → build → live URL a
 * single query.
 */
import { trackUxEvent } from "./ux-telemetry.ts";

const YM_ID = Number(process.env.NEXT_PUBLIC_YM_ID ?? "110158915");

type MetrikaFn = (id: number, action: string, goal?: string, params?: Record<string, string>) => void;

/** Reads the `utm_source` value out of a CTA href, or "direct" when absent. */
export function ctaSource(href: string): string {
  const match = /[?&]utm_source=([^&]+)/.exec(href);
  return match ? decodeURIComponent(match[1]) : "direct";
}

/**
 * Sends a Metrika goal and mirrors it into `ux_events`.
 *
 * The mirror runs BEFORE the counter check on purpose: a visitor running an ad
 * blocker never loads `ym`, and those visitors are exactly the ones Metrika
 * silently drops from every funnel. Our own ingest still records them.
 *
 * No-op for Metrika when the counter has not loaded (ad blockers, SSR, local
 * dev) so a missing counter can never break a CTA click.
 */
export function reachGoal(goal: string, params?: Record<string, string>) {
  if (typeof window === "undefined") return;
  trackUxEvent("goal", goal, params ?? {});
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

/** Goal fired once a returning OIDC callback completes a fresh registration. */
export const GOAL_REGISTRATION_COMPLETE = "registration_complete";

/**
 * Goal fired when an OIDC round-trip lands back on /callback without a
 * session. Makes the sign-in dead end countable: until this existed the
 * drop-off was invisible, since a visitor who never gets a token never
 * reaches an authenticated API call and so leaves no audit trail either.
 */
export const GOAL_AUTH_CALLBACK_FAILED = "auth_callback_failed";

/** Goal fired the first time an app of this user reaches a running phase. */
export const GOAL_DEPLOY_SUCCESS = "deploy_success";

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

/** Name of the first-party cookie carrying the `utm_medium` of the first touch. */
export const MEDIUM_COOKIE = "dada_med";

/** Name of the first-party cookie carrying the `utm_campaign` of the first touch. */
export const CAMPAIGN_COOKIE = "dada_cmp";

/** Name of the first-party cookie carrying the raw referrer of the first touch. */
export const REFERRER_COOKIE = "dada_ref";

const ATTRIBUTION_MAX_AGE = 60 * 60 * 24 * 90;
const SOURCE_MAX_LEN = 64;
const MEDIUM_MAX_LEN = 64;
const CAMPAIGN_MAX_LEN = 64;
const REFERRER_MAX_LEN = 255;

/** Raw, untrusted inputs `computeAttribution` derives the first-touch cookies from. */
export interface AttributionSignals {
  /** `location.search` of the landing page (including the leading `?`, or empty). */
  search: string;
  /** `document.referrer` as read from the browser, or empty when absent. */
  referrer: string;
  /** `location.hostname` of the landing page, used to detect same-site referrers. */
  hostname: string;
}

/** The four values `rememberAttribution` writes, one per sibling cookie. */
export interface AttributionValues {
  source: string;
  medium: string;
  campaign: string;
  ref: string;
}

/**
 * Strips ASCII control characters (including DEL) so a crafted query string or
 * referrer can never smuggle non-printable bytes into a cookie value.
 */
function stripNonPrintable(raw: string): string {
  return raw.replace(/[\x00-\x1F\x7F]/g, "");
}

/** Cleans and caps a raw attribution value before it is URL-encoded. */
function clamp(raw: string, maxLen: number): string {
  return stripNonPrintable(raw).slice(0, maxLen);
}

/** Hostname of `referrer`, or empty when `referrer` is absent or unparsable. */
function referrerHostname(referrer: string): string {
  if (!referrer) return "";
  try {
    return new URL(referrer).hostname;
  } catch {
    return "";
  }
}

/**
 * Derives the four first-touch attribution values from raw signals, with no
 * DOM access -- kept pure so the attribution logic can be unit tested without
 * a browser.
 *
 * Precedence for `source`: `utm_source`, then the referrer's hostname when the
 * referrer points at a different site, then the literal `direct`. A same-site
 * referrer (internal navigation) is never treated as an acquisition source,
 * and `ref` is likewise blanked out for it so an internal hop can never look
 * like an external one in the data.
 */
export function computeAttribution(signals: AttributionSignals): AttributionValues {
  const params = new URLSearchParams(signals.search);
  const utmSource = params.get("utm_source") ?? "";
  const refHost = referrerHostname(signals.referrer);
  const sameSite = refHost !== "" && refHost === signals.hostname;
  const externalRefHost = refHost && !sameSite ? refHost : "";

  const source = utmSource || externalRefHost || "direct";
  const medium = params.get("utm_medium") ?? "";
  const campaign = params.get("utm_campaign") ?? "";
  const ref = sameSite ? "" : signals.referrer;

  return {
    source: clamp(source, SOURCE_MAX_LEN),
    medium: clamp(medium, MEDIUM_MAX_LEN),
    campaign: clamp(campaign, CAMPAIGN_MAX_LEN),
    ref: clamp(ref, REFERRER_MAX_LEN),
  };
}

/** Writes one attribution cookie with the same attrs `dada_src` has always used. */
function setAttributionCookie(name: string, value: string, hostname: string): void {
  const safe = encodeURIComponent(value);
  const domain = hostname.endsWith("dada-tuda.ru") ? "; domain=.dada-tuda.ru" : "";
  document.cookie = `${name}=${safe}; path=/; max-age=${ATTRIBUTION_MAX_AGE}; samesite=lax${domain}`;
}

/**
 * Captures the visitor's FIRST touch into four sibling cookies (`dada_src`,
 * `dada_med`, `dada_cmp`, `dada_ref`) that the backend reads at signup time to
 * attribute a registration to the page that actually brought the visitor in.
 *
 * Must run on every page load, as early as possible, NOT just on `/register`:
 * an organic visitor who lands on a marketing page and only reaches
 * `/register` several clicks later would otherwise carry no `utm_source` at
 * all by the time `rememberSource` used to run, and stayed unattributed
 * forever.
 *
 * First-touch-wins: if `dada_src` is already set, this writes nothing, so a
 * later internal page view can never overwrite the real first touch. This is
 * a deliberate behaviour change from the old `rememberSource`-only flow:
 * `direct` is now WRITTEN (not skipped) on the first call, because skipping
 * it left organic visitors permanently unattributed and vulnerable to a later
 * call overwriting them with whatever page they happened to land on next.
 *
 * No-op during SSR (`typeof document === "undefined"`).
 */
export function rememberAttribution(): void {
  if (typeof document === "undefined") return;
  if (document.cookie.split("; ").some((c) => c.startsWith(`${SOURCE_COOKIE}=`))) return;
  const hostname = document.location.hostname;
  const values = computeAttribution({
    search: document.location.search,
    referrer: document.referrer,
    hostname,
  });
  setAttributionCookie(SOURCE_COOKIE, values.source, hostname);
  setAttributionCookie(MEDIUM_COOKIE, values.medium, hostname);
  setAttributionCookie(CAMPAIGN_COOKIE, values.campaign, hostname);
  setAttributionCookie(REFERRER_COOKIE, values.ref, hostname);
}

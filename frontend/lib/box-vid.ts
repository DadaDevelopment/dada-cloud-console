/**
 * `dada_vid` — the anonymous visitor id for the Dada Box fake door.
 *
 * WHY A SECOND COOKIE. `dada_uid` (lib/uid-cookie.ts,
 * docs/architecture/yandex-metrika-uid-cookie.md) cannot be reused here: it is
 * only ever published for an *authenticated* console user, and the /box landing
 * is anonymous marketing traffic. Without an id of its own, every funnel counter
 * counts events instead of people — one curious visitor replaying the demo six
 * times looks like six visitors, and there is no key to deduplicate the page
 * views that form the conversion denominator.
 *
 * WHAT IT MAY CONTAIN. An opaque UUID and nothing else. Never an email, never a
 * username, never anything derived from one (152-ФЗ: no personal data leaves the
 * perimeter through analytics). The backend enforces this rather than trusting it:
 * POST /api/v1/box/leads rejects any `vid` that does not parse as a UUID, so the
 * column cannot hold an address even if a client hand-rolls the request.
 *
 * ATTRIBUTES, and where they differ from `dada_uid`:
 *
 * | Field    | dada_vid                        | why                                          |
 * |----------|---------------------------------|----------------------------------------------|
 * | Value    | opaque UUID v4                  | no PII, no join back to a person             |
 * | Max-Age  | 400 days                        | the ceiling Chrome will honour for a cookie  |
 * | SameSite | Lax                             | same as dada_uid; survives a normal inbound link |
 * | Secure   | on https                        | as documented for dada_uid, omitted on http dev |
 * | HttpOnly | true (dada_uid is false)        | nothing in the browser reads this one        |
 * | Path     | /                               | one id across /box and /en/box               |
 * | Domain   | host-only                       | this id belongs to the marketing host alone  |
 *
 * HttpOnly is the deliberate difference. `dada_uid` must stay JS-readable because
 * the Metrika snippet reads it with document.cookie. Nothing reads `dada_vid` from
 * the browser: the funnel sink is a same-origin route handler, so the cookie is
 * attached to the request automatically and read server-side. Making it HttpOnly
 * costs nothing and takes the id out of reach of any third-party script on the page.
 *
 * Domain is host-only for the same reason: the fleet-wide `.dada-tuda.ru` scope
 * exists so one login reaches every static frontend, which is a requirement
 * `dada_vid` does not have. Keeping it host-only means an anonymous marketing id
 * is never broadcast to the console or to any other subdomain.
 */

/** Cookie name. Fixed, not configurable: the backend and the runbook both name it. */
export const BOX_VID_COOKIE = "dada_vid";

/** 400 days, the maximum a browser will retain (Chrome caps Max-Age at 400 days). */
export const BOX_VID_MAX_AGE = 400 * 24 * 60 * 60;

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * True when value is a plain UUID. Used before echoing a cookie value into a
 * funnel record, so a tampered or truncated cookie is replaced rather than stored.
 */
export function isBoxVid(value: string | undefined | null): boolean {
  return typeof value === "string" && UUID_RE.test(value);
}

/** Mints a fresh opaque visitor id. */
export function newBoxVid(): string {
  return crypto.randomUUID();
}

/**
 * Reads a valid `dada_vid` out of a raw Cookie header, or undefined when it is
 * absent or not an opaque id. Used by the funnel route handler, which reads the
 * cookie the browser sent rather than trusting a value in the request body.
 */
export function readBoxVid(cookieHeader: string | null | undefined): string | undefined {
  if (!cookieHeader) return undefined;
  for (const part of cookieHeader.split(";")) {
    const eq = part.indexOf("=");
    if (eq < 0) continue;
    if (part.slice(0, eq).trim() !== BOX_VID_COOKIE) continue;
    const value = decodeURIComponent(part.slice(eq + 1).trim());
    return isBoxVid(value) ? value : undefined;
  }
  return undefined;
}

/**
 * Serializes the Set-Cookie value for a visitor id. `secure` is false only for
 * plain-http local development, matching the rule already documented for
 * `dada_uid` ("true on https, omitted on http://localhost dev only").
 */
export function boxVidSetCookie(vid: string, secure: boolean): string {
  const attrs = [
    `${BOX_VID_COOKIE}=${vid}`,
    "Path=/",
    `Max-Age=${BOX_VID_MAX_AGE}`,
    "SameSite=Lax",
    "HttpOnly",
  ];
  if (secure) attrs.push("Secure");
  return attrs.join("; ");
}

/** True for the Box landing paths (RU at /box, EN at /en/box). */
export function isBoxLandingPath(pathname: string): boolean {
  return pathname === "/box" || pathname === "/en/box";
}

"use client";

/**
 * Publishes the authenticated user's internal, non-PII id into a JS-readable
 * cookie so static/edge frontends across the fleet can bind it to Yandex.Metrika
 * (ym setUserID) without owning an auth context of their own.
 *
 * The value MUST be an internal, non-public id only (OIDC `sub` / internal user
 * id) — NEVER email, username, or display name. Sending PII to Yandex would
 * violate 152-ФЗ. Callers pass `principal.sub` (a Keycloak UUID) or the local
 * `user.id`; both are opaque internal ids.
 *
 * The cookie is deliberately NOT HttpOnly (the ya-metrika snippet reads it via
 * document.cookie), is Secure on https, SameSite=Lax, path `/`, and — on the
 * dada-tuda.ru apex — scoped to `.dada-tuda.ru` so a single login publishes the
 * id to every same-domain static frontend. The name matches the fleet-wide helm
 * value `analytics.yandexMetrika.uidCookie`.
 */

const UID_COOKIE_NAME = process.env.NEXT_PUBLIC_UID_COOKIE_NAME ?? "dada_uid";
const UID_COOKIE_MAX_AGE = 60 * 60 * 24 * 30;

function cookieDomain(hostname: string): string {
  return /(^|\.)dada-tuda\.ru$/.test(hostname) ? ".dada-tuda.ru" : "";
}

function baseAttrs(): string {
  const domain = cookieDomain(location.hostname);
  const secure = location.protocol === "https:" ? "; Secure" : "";
  return `Path=/; SameSite=Lax${domain ? `; Domain=${domain}` : ""}${secure}`;
}

/**
 * Sets or clears the `dada_uid` cookie. Pass the internal non-PII id to set it;
 * pass a falsy value (logout / unauthenticated) to clear it. No-op during SSR.
 */
export function publishUid(uid: string | null | undefined): void {
  if (typeof document === "undefined") return;
  const attrs = baseAttrs();
  if (uid) {
    document.cookie = `${UID_COOKIE_NAME}=${encodeURIComponent(uid)}; Max-Age=${UID_COOKIE_MAX_AGE}; ${attrs}`;
  } else {
    document.cookie = `${UID_COOKIE_NAME}=; Max-Age=0; ${attrs}`;
  }
}

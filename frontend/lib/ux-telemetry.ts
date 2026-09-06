"use client";

/**
 * Client-side UX telemetry: the browser half of the end-to-end user path.
 *
 * The backend's `audit_events` journal only records authorized WRITE actions,
 * so everything a person does that never reaches a mutating endpoint -- opening
 * a page, poking Settings tabs, opening and closing a modal, pressing a button
 * that did nothing -- left no trace anywhere. This module reports those events
 * to `POST /api/v1/telemetry/events` (backend/internal/api/ux_events.go), which
 * stores them in `ux_events` (migration 069).
 *
 * IDENTITY. `anon_id` is a UUID minted here and kept in localStorage, so it
 * survives login and stitches the pre-signup visit to the account. `session_id`
 * lives in sessionStorage and scopes one visit. The logged-in user is NOT sent:
 * the backend resolves it from the `dada_uid` cookie (lib/uid-cookie.ts), the
 * same non-PII id published to Yandex.Metrika.
 *
 * PRIVACY (152-ФЗ). Only control names, roles and paths are reported. Field
 * VALUES are never read: `input.value` is not touched anywhere in this file,
 * password fields are ignored entirely, and query strings are stripped from the
 * reported path.
 *
 * FAIL-OPEN. Every entry point swallows its errors through `ignore()` so
 * telemetry can never break the app: blocked storage, a missing
 * crypto.randomUUID, or a dead endpoint all degrade to "no events" rather than
 * to an exception inside a click handler.
 */

const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL ?? "";
const INGEST_PATH = "/api/v1/telemetry/events";

/**
 * The console host is the only one whose ingress routes `/api/v1/*` to the
 * backend. The marketing site runs the same image on `cloud.dada-tuda.ru`,
 * where a relative POST answers 404 -- which silently threw away exactly the
 * pre-login half of the path this module exists to capture (verified live:
 * `fetch('/api/v1/telemetry/events')` on cloud.dada-tuda.ru returns 404).
 *
 * Both hosts are same-site under dada-tuda.ru, so the SameSite=Lax `dada_uid`
 * cookie still travels and the user is still resolved server-side.
 */
const CONSOLE_ORIGIN = "https://console.dada-tuda.ru";
const API_HOST = "console.dada-tuda.ru";

/**
 * Resolves the ingest endpoint for the current host. An explicit
 * NEXT_PUBLIC_API_URL wins (non-prod targets), then any host that is not the
 * console gets the absolute console origin, and everything else stays
 * relative -- which keeps localhost dev on its own dev server.
 */
function ingestEndpoint(): string {
  if (API_BASE_URL) return `${API_BASE_URL}${INGEST_PATH}`;
  if (typeof location !== "undefined" && /\.dada-tuda\.ru$/.test(location.hostname)) {
    if (location.hostname !== API_HOST) return `${CONSOLE_ORIGIN}${INGEST_PATH}`;
  }
  return INGEST_PATH;
}

const ANON_STORAGE_KEY = "dada_ux_aid";
const SESSION_STORAGE_KEY = "dada_ux_sid";

const FLUSH_INTERVAL_MS = 4000;
const FLUSH_AT_COUNT = 20;
const QUEUE_MAX = 60;
const TARGET_MAX = 200;
const PATH_MAX = 500;

export type UxEventType =
  | "session_start"
  | "pageview"
  | "click"
  | "input_commit"
  | "nav_leave"
  | "visibility"
  | "error_shown"
  | "goal"
  | "view"
  | "consent";

interface UxEvent {
  type: UxEventType;
  path: string;
  target: string;
  props: Record<string, unknown>;
  at: string;
}

let queue: UxEvent[] = [];
let timer: ReturnType<typeof setTimeout> | null = null;
let listenersBound = false;
let lastPageviewPath = "";

/** Explicit no-op, so every fail-open catch block has a body. */
const ignore = (): void => undefined;

function uuid(): string {
  try {
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
      return crypto.randomUUID();
    }
  } catch {
    ignore();
  }
  return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (ch) => {
    const r = (Math.random() * 16) | 0;
    const v = ch === "x" ? r : (r & 0x3) | 0x8;
    return v.toString(16);
  });
}

function readStoredId(store: Storage | undefined, key: string): string {
  if (!store) return "";
  try {
    const existing = store.getItem(key);
    if (existing) return existing;
    const minted = uuid();
    store.setItem(key, minted);
    return minted;
  } catch {
    return "";
  }
}

function anonId(): string {
  if (typeof window === "undefined") return "";
  return readStoredId(window.localStorage, ANON_STORAGE_KEY);
}

function sessionId(): string {
  if (typeof window === "undefined") return "";
  return readStoredId(window.sessionStorage, SESSION_STORAGE_KEY);
}

/** Current pathname with the query string stripped -- it can carry tokens. */
function currentPath(): string {
  if (typeof location === "undefined") return "";
  return location.pathname.slice(0, PATH_MAX);
}

/**
 * Sends one batch. The body is labelled `text/plain` on purpose: that keeps the
 * cross-origin POST from the marketing host a CORS "simple request" with no
 * preflight, which sendBeacon cannot perform at all. The backend binds JSON
 * explicitly and does not read the content type. Responses are never read, so
 * the missing CORS response headers cost nothing.
 */
function post(body: string): void {
  const endpoint = ingestEndpoint();
  try {
    if (typeof navigator !== "undefined" && typeof navigator.sendBeacon === "function") {
      const ok = navigator.sendBeacon(endpoint, new Blob([body], { type: "text/plain" }));
      if (ok) return;
    }
    void fetch(endpoint, {
      method: "POST",
      headers: { "Content-Type": "text/plain" },
      body,
      credentials: "include",
      keepalive: true,
    }).catch(ignore);
  } catch {
    ignore();
  }
}

/**
 * Sends whatever is queued and clears it. Safe to call at any time. A failed
 * flush is dropped rather than retried: the point is the path, not delivery
 * guarantees, and an infinite retry would outlive the page.
 */
export function flushUxEvents(): void {
  if (timer) {
    clearTimeout(timer);
    timer = null;
  }
  if (queue.length === 0) return;
  const events = queue;
  queue = [];
  try {
    post(
      JSON.stringify({
        anon_id: anonId(),
        session_id: sessionId(),
        events,
      }),
    );
  } catch {
    ignore();
  }
}

function scheduleFlush(): void {
  if (timer) return;
  timer = setTimeout(() => {
    timer = null;
    flushUxEvents();
  }, FLUSH_INTERVAL_MS);
}

/**
 * Queues one UX event. Never throws; drops the oldest events when the queue is
 * full so a runaway emitter cannot grow memory without bound.
 */
export function trackUxEvent(
  type: UxEventType,
  target = "",
  props: Record<string, unknown> = {},
): void {
  try {
    if (typeof window === "undefined") return;
    queue.push({
      type,
      path: currentPath(),
      target: String(target).slice(0, TARGET_MAX),
      props,
      at: new Date().toISOString(),
    });
    if (queue.length > QUEUE_MAX) queue = queue.slice(-QUEUE_MAX);
    if (queue.length >= FLUSH_AT_COUNT) {
      flushUxEvents();
      return;
    }
    scheduleFlush();
  } catch {
    ignore();
  }
}

/** Records a route change, deduplicated against the previous one. */
export function trackPageview(path: string): void {
  const p = (path || currentPath()).slice(0, PATH_MAX);
  if (p === lastPageviewPath) return;
  const from = lastPageviewPath;
  lastPageviewPath = p;
  trackUxEvent("pageview", p, from ? { from } : {});
}

const LABEL_MAX = 40;

const IDENTIFIER_TOKEN =
  /(^|\s)(\S*@\S*|\S*\.(ru|com|net|org|io|dev|app|local|cloud)\b\S*|[0-9a-f]{8}-[0-9a-f]{4}\S*|\S*[-.][a-z0-9]*(gmail|yandex|mail|icloud|outlook|proton)[-.]\S*|\S*\d{4,}\S*)(?=\s|$)/gi;

/**
 * Strips anything that could name a person or a resource out of a click label.
 *
 * Labels are read off the page, so whatever the page renders can end up in
 * telemetry: a prod row read `div:fonbet-valueReadynexus.dada-tuda.ru/artem<...>-gmail-com`
 * -- one app row's whole text, carrying the owner's email in a registry path.
 * A telemetry column is the wrong place for that under 152-FZ, and it is also
 * unusable for analysis: every row becomes its own unique target.
 *
 * Anything that looks like an address, a host, a uuid or a long number is
 * dropped rather than masked, because a masked identifier is still one bucket
 * per user. An empty result means the caller falls back to a structural name.
 */
export function sanitizeLabel(raw: string): string {
  const cleaned = raw.replace(IDENTIFIER_TOKEN, " ").replace(/\s+/g, " ").trim();
  return cleaned.slice(0, LABEL_MAX);
}

/**
 * Reads the control's own words, not its subtree's.
 *
 * `textContent` on a row-shaped control (a card with role="button") returns
 * every descendant's text glued together, which is both a leak and a label
 * nobody can group by. Direct child text nodes are what the author actually
 * wrote on this control.
 */
function ownText(el: Element): string {
  let out = "";
  el.childNodes.forEach((node) => {
    if (node.nodeType === 3) out += node.nodeValue ?? "";
  });
  return out.replace(/\s+/g, " ").trim();
}

function textLabel(el: Element): string {
  const aria = el.getAttribute("aria-label");
  if (aria) return sanitizeLabel(aria);
  const title = el.getAttribute("title");
  if (title) return sanitizeLabel(title);
  const own = ownText(el);
  if (own) return sanitizeLabel(own);
  const text = (el.textContent ?? "").replace(/\s+/g, " ").trim();
  if (text.length > LABEL_MAX * 2) return "";
  return sanitizeLabel(text);
}

/**
 * Describes the clicked control by NAME, never by content the user typed.
 *
 * Preference order: an explicit `data-ux` marker, then `data-testid`, then the
 * accessible label, sanitized. For fields only the tag/type/name is reported --
 * the value is never read, and password fields return "" so the click is
 * dropped entirely.
 *
 * A control with no safe label keeps its structural name (`div[role=button]`)
 * instead of borrowing its children's text: the click still counts, it just
 * stops carrying whoever was rendered inside it.
 */
function describeTarget(el: Element): { target: string; props: Record<string, unknown> } {
  const marked = el.closest("[data-ux]");
  if (marked) {
    return { target: marked.getAttribute("data-ux") ?? "", props: { kind: "marked" } };
  }

  const control = el.closest(
    "button, a, [role='button'], [role='tab'], [role='menuitem'], summary, label, select, input, textarea",
  );
  if (!control) return { target: "", props: {} };

  const tag = control.tagName.toLowerCase();
  if (tag === "input" || tag === "textarea" || tag === "select") {
    const type = control.getAttribute("type") ?? tag;
    if (type === "password") return { target: "", props: {} };
    const name =
      control.getAttribute("name") ??
      control.getAttribute("id") ??
      control.getAttribute("aria-label") ??
      control.getAttribute("placeholder") ??
      "";
    const named = name ? sanitizeLabel(name) : "";
    return { target: `${tag}:${type}${named ? `:${named}` : ""}`, props: { kind: "field" } };
  }

  const testid = control.getAttribute("data-testid");
  const label = testid || textLabel(control);
  const props: Record<string, unknown> = { kind: tag };
  if (tag === "a") {
    const href = control.getAttribute("href") ?? "";
    if (href.startsWith("/")) props.href = href.split("?")[0].slice(0, PATH_MAX);
    else if (href.startsWith("#")) props.href = href.slice(0, PATH_MAX);
    else if (href) props.href = "external";
  }
  const disabled =
    control.hasAttribute("disabled") || control.getAttribute("aria-disabled") === "true";
  if (disabled) props.disabled = true;
  const role = control.getAttribute("role");
  return { target: label ? `${tag}:${label}` : role ? `${tag}[${role}]` : tag, props };
}

function onDocumentClick(ev: Event): void {
  try {
    const el = ev.target as Element | null;
    if (!el || typeof el.closest !== "function") return;
    const { target, props } = describeTarget(el);
    if (!target) return;
    trackUxEvent("click", target, props);
  } catch {
    ignore();
  }
}

function onVisibility(): void {
  try {
    if (document.visibilityState === "hidden") {
      trackUxEvent("visibility", "hidden");
      flushUxEvents();
    } else {
      trackUxEvent("visibility", "visible");
    }
  } catch {
    ignore();
  }
}

function onPageHide(): void {
  try {
    trackUxEvent("nav_leave", currentPath());
    flushUxEvents();
  } catch {
    ignore();
  }
}

/**
 * Binds the global listeners once per document and emits `session_start` for a
 * fresh sessionStorage session. Idempotent: repeated calls (React strict mode,
 * remounts) bind nothing twice.
 */
export function startUxTelemetry(): void {
  try {
    if (typeof window === "undefined" || listenersBound) return;
    listenersBound = true;

    let fresh = true;
    try {
      fresh = !window.sessionStorage.getItem(SESSION_STORAGE_KEY);
    } catch {
      ignore();
    }
    sessionId();
    if (fresh) {
      trackUxEvent("session_start", currentPath(), {
        referrer: (document.referrer || "").slice(0, 200),
        lang: navigator.language ?? "",
        w: window.innerWidth,
        h: window.innerHeight,
      });
    }

    document.addEventListener("click", onDocumentClick, true);
    document.addEventListener("visibilitychange", onVisibility);
    window.addEventListener("pagehide", onPageHide);
  } catch {
    ignore();
  }
}

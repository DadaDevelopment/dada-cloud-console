// Client helpers for the /box fake-door funnel.
//
// Four events, in ascending order of intent (see docs/product/box-product-brief.md):
//   page_view         — traffic. Server-side, once per session, deduplicated by
//                       the dada_vid cookie. It exists so the conversion
//                       denominator lives in the same store as the numerator
//                       instead of in Yandex Metrika, where it could only be
//                       compared by hand and argued about.
//   demo_run          — played the scripted demo (curiosity)
//   box_requested     — left a request with intent (this is the door)
//   crystallize_intent— asked for the move to a permanent VM (validates the ladder)
//
// crystallize_intent is the metric that decides whether Box is a product with a
// ladder or a one-off utility, so it gets its own event rather than a form field.
//
// The visitor id is NOT handled here: it is an HttpOnly cookie issued by proxy.ts
// and read server-side (lib/box-vid.ts). Nothing in the browser needs to see it,
// and keeping it out of JS keeps it away from third-party scripts.

const ENDPOINT = "/api/box/lead";

/**
 * The door's own utm tag. Named door_box to sit alongside the existing door tests
 * (`utm_source=door_b` on /deploy-vibe-coding) so the brief's "compare conversion
 * with the other door tests" is a query rather than a project.
 */
export const BOX_UTM_SOURCE = "door_box";

/** sessionStorage key guarding the once-per-session page_view. */
const PAGE_VIEW_KEY = "dada_box_page_view";

export interface BoxLeadInput {
  email: string;
  contact?: string;
  agent?: string;
  parallel?: string;
  useCase: string;
  price?: string;
  locale: string;
}

export interface BoxLeadResult {
  claim: string;
}

/**
 * The acquisition source to attribute this visit to: an inbound `utm_source` when
 * the visitor arrived with one, otherwise the door's own tag. Capped and stripped
 * of anything that is not a plain tag so it cannot smuggle content into a report.
 */
export function boxUtmSource(): string {
  if (typeof window === "undefined") return BOX_UTM_SOURCE;
  const inbound = new URLSearchParams(window.location.search).get("utm_source");
  if (!inbound) return BOX_UTM_SOURCE;
  const clean = inbound.replace(/[^A-Za-z0-9_.-]/g, "").slice(0, 64);
  return clean || BOX_UTM_SOURCE;
}

/** Fire-and-forget funnel signal. Never throws — a failed beacon must not break the page. */
export function reportBoxEvent(payload: Record<string, unknown>): void {
  void fetch(ENDPOINT, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ utmSource: boxUtmSource(), ...payload }),
    keepalive: true,
  }).catch(() => {});
}

/**
 * Reports the landing page view once per browser session.
 *
 * Two layers of deduplication, on purpose. This sessionStorage guard stops a
 * client-side navigation or a remount from re-firing; the backend then dedups by
 * dada_vid inside a 30-minute window, which is what actually holds when
 * sessionStorage is unavailable or a visitor opens three tabs. The client guard is
 * a courtesy, not the guarantee.
 */
export function reportBoxPageView(locale: string): void {
  if (typeof window === "undefined") return;
  try {
    if (window.sessionStorage.getItem(PAGE_VIEW_KEY)) return;
    window.sessionStorage.setItem(PAGE_VIEW_KEY, "1");
  } catch {
    // Private mode / blocked storage: fall through and let the server dedup.
  }
  reportBoxEvent({ event: "page_view", locale });
}

/** Submits the access request. Throws on failure so the form can show an error. */
export async function submitBoxLead(input: BoxLeadInput): Promise<BoxLeadResult> {
  const res = await fetch(ENDPOINT, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ event: "box_requested", utmSource: boxUtmSource(), ...input }),
  });
  if (!res.ok) throw new Error(`lead failed: ${res.status}`);
  const data = (await res.json()) as Partial<BoxLeadResult>;
  if (!data.claim) throw new Error("lead failed: no claim code");
  return { claim: data.claim };
}

/** Records which parts of crystallization the person actually needs. */
export function reportCrystallizeIntent(claim: string, wants: string[], locale: string): void {
  reportBoxEvent({ event: "crystallize_intent", claim, wants, locale });
}

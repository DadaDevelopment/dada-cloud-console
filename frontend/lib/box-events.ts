// Client helpers for the /box fake-door funnel.
//
// Four events, in ascending order of intent (see docs/product/box-product-brief.md):
//   view              — page view, covered by existing site analytics
//   demo_run          — played the scripted demo (curiosity)
//   box_requested     — left a request with intent (this is the door)
//   crystallize_intent— asked for the move to a permanent VM (validates the ladder)
//
// crystallize_intent is the metric that decides whether Box is a product with a
// ladder or a one-off utility, so it gets its own event rather than a form field.

const ENDPOINT = "/api/box/lead";

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

/** Fire-and-forget funnel signal. Never throws — a failed beacon must not break the page. */
export function reportBoxEvent(payload: Record<string, unknown>): void {
  void fetch(ENDPOINT, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
    keepalive: true,
  }).catch(() => {});
}

/** Submits the access request. Throws on failure so the form can show an error. */
export async function submitBoxLead(input: BoxLeadInput): Promise<BoxLeadResult> {
  const res = await fetch(ENDPOINT, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ event: "box_requested", ...input }),
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

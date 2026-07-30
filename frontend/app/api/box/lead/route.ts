import { NextResponse } from "next/server";
import { boxVidSetCookie, newBoxVid, readBoxVid } from "@/lib/box-vid";

/**
 * Funnel sink for the Dada Box private-preview fake door (/box, /en/box).
 *
 * Events are forwarded to the backend (POST /api/v1/box/leads), which writes them
 * to box_funnel_events / box_leads. That is the system of record — a log line
 * could not answer the questions the experiment exists for (see
 * docs/plans/2026-07-29-box-test-and-measurement.md §6): no denominator, no
 * visitor identity, and therefore no trustworthy conversion ratio.
 *
 * The log line and the webhook stay, deliberately, as a FAIL-OPEN FALLBACK. This
 * handler's original stance was that the page must never break because of our own
 * plumbing, and forwarding must not weaken it: the claim code is still minted here
 * and returned to the visitor whether or not the backend answered, and a storage
 * failure is logged, not surfaced.
 *
 * PII: the log line carries metadata only. The email the person typed is delivered
 * to the operator through the webhook/Telegram channel and stored in box_leads —
 * never written to stdout, which is shipped to OpenSearch and retained for weeks.
 *
 * Env:
 *   BOX_LEADS_BACKEND_ORIGIN   backend origin for the durable store (optional;
 *                              falls back to NEXT_PUBLIC_CONSOLE_URL)
 *   BOX_LEADS_WEBHOOK_URL      generic JSON POST target (optional)
 *   BOX_LEADS_TELEGRAM_TOKEN   bot token (optional, needs CHAT too)
 *   BOX_LEADS_TELEGRAM_CHAT    chat id to notify (optional)
 */

export const dynamic = "force-dynamic";

// page_view is a first-class server-side event, not something left to Yandex
// Metrika: with the denominator in a different system from the numerator, the
// view -> request conversion is a ratio nobody can check, and it gets argued about
// instead of used. It is deduplicated per dada_vid per session by the backend.
const KNOWN_EVENTS = new Set(["page_view", "demo_run", "box_requested", "crystallize_intent"]);

// Events that must never page a human: high volume, low signal.
const QUIET_EVENTS = new Set(["page_view", "demo_run"]);

const BACKEND_ORIGIN = (
  process.env.BOX_LEADS_BACKEND_ORIGIN ??
  process.env.NEXT_PUBLIC_CONSOLE_URL ??
  "https://console.dada-tuda.ru"
).replace(/\/$/, "");

// Field caps: this endpoint is unauthenticated, so bound everything that gets
// logged or forwarded rather than trusting the client.
const MAX_FIELD = 500;
const MAX_USE_CASE = 2000;

const RATE_LIMIT = 20;
const RATE_WINDOW_MS = 10 * 60 * 1000;
const hits = new Map<string, { count: number; resetAt: number }>();

function rateLimited(ip: string): boolean {
  const now = Date.now();
  const entry = hits.get(ip);
  if (!entry || now > entry.resetAt) {
    hits.set(ip, { count: 1, resetAt: now + RATE_WINDOW_MS });
    // Opportunistic sweep so the map can't grow unbounded on a long-lived instance.
    if (hits.size > 5000) {
      for (const [key, value] of hits) if (now > value.resetAt) hits.delete(key);
    }
    return false;
  }
  entry.count += 1;
  return entry.count > RATE_LIMIT;
}

function clientIp(req: Request): string {
  const fwd = req.headers.get("x-forwarded-for");
  if (fwd) return fwd.split(",")[0].trim();
  return req.headers.get("x-real-ip") ?? "unknown";
}

function str(value: unknown, max = MAX_FIELD): string | undefined {
  if (typeof value !== "string") return undefined;
  const trimmed = value.trim();
  return trimmed ? trimmed.slice(0, max) : undefined;
}

/** Human-readable request code, e.g. BOX-7F3A-9C21. Shown to the person and used to join later events. */
function claimCode(): string {
  const hex = crypto.randomUUID().replace(/-/g, "").toUpperCase();
  return `BOX-${hex.slice(0, 4)}-${hex.slice(4, 8)}`;
}

export async function POST(req: Request) {
  const ip = clientIp(req);
  if (rateLimited(ip)) {
    return NextResponse.json({ error: "rate_limited" }, { status: 429 });
  }

  let body: Record<string, unknown>;
  try {
    body = (await req.json()) as Record<string, unknown>;
  } catch {
    return NextResponse.json({ error: "bad_json" }, { status: 400 });
  }

  const event = str(body.event, 40);
  if (!event || !KNOWN_EVENTS.has(event)) {
    return NextResponse.json({ error: "unknown_event" }, { status: 400 });
  }

  // The visitor id is read from the cookie the browser sent, never from the body:
  // the body is client-controlled, and a vid a caller can choose is a vid a caller
  // can collide with. The proxy issues it on the first hit of /box; minting one
  // here covers a request that somehow arrives without it (cookie blocked, direct
  // POST) so the event still carries an identity.
  const cookieVid = readBoxVid(req.headers.get("cookie"));
  const vid = cookieVid ?? newBoxVid();

  const locale = str(body.locale, 8) ?? "ru";
  const utmSource = str(body.utmSource, 120) ?? str(body.utm_source, 120);
  const referer = str(req.headers.get("referer") ?? undefined);

  const record: Record<string, unknown> = {
    at: new Date().toISOString(),
    event,
    locale,
    vid,
    utmSource,
    referer,
    ua: str(req.headers.get("user-agent") ?? undefined, 300),
  };

  let claim: string | undefined;
  let email: string | undefined;
  let contact: string | undefined;
  let useCase: string | undefined;
  let wants: string[] = [];

  if (event === "box_requested") {
    email = str(body.email, 200);
    useCase = str(body.useCase, MAX_USE_CASE);
    if (!email || !useCase) {
      return NextResponse.json({ error: "missing_fields" }, { status: 400 });
    }
    contact = str(body.contact);
    // The claim code is minted HERE, not by the backend. The visitor must receive
    // a real code even when the durable store is unreachable — that is what makes
    // the storage hop non-load-bearing for the page.
    claim = claimCode();
    Object.assign(record, {
      claim,
      agent: str(body.agent, 60),
      parallel: str(body.parallel, 60),
      price: str(body.price, 60),
    });
  }

  if (event === "crystallize_intent") {
    wants = Array.isArray(body.wants)
      ? body.wants.filter((w): w is string => typeof w === "string").slice(0, 20).map((w) => w.slice(0, MAX_FIELD))
      : [];
    claim = str(body.claim, 40);
    Object.assign(record, { claim, wants });
  }

  const stored = await storeEvent({
    event,
    claim,
    vid,
    locale,
    utm_source: utmSource,
    referer,
    email,
    contact,
    agent: str(body.agent, 60),
    parallel: str(body.parallel, 60),
    price: str(body.price, 60),
    use_case: useCase,
    wants,
  }, ip);

  // Fallback record. Single-line JSON so it survives log aggregation and can be
  // grepped by event name. `stored` says whether the durable write succeeded, so
  // a gap in the tables can be reconciled from logs instead of guessed at.
  //
  // No email and no contact here: stdout is shipped to OpenSearch and retained,
  // and this experiment's PII rule is that the address the person typed lives in
  // box_leads and in the operator's notification, nowhere else. The booleans are
  // enough to notice a lead that failed to store.
  console.log(
    `box_funnel ${JSON.stringify({
      ...record,
      stored,
      has_email: Boolean(email),
      has_contact: Boolean(contact),
    })}`,
  );

  // The operator's channel keeps the full record, email included: it is a direct
  // notification to a person who has to reply to the lead, not a log sink.
  const notifiable = { ...record, email, contact, useCase };
  await Promise.allSettled([notifyWebhook(notifiable), notifyTelegram(notifiable)]);

  const res = NextResponse.json(claim ? { ok: true, claim } : { ok: true });
  if (!cookieVid) {
    // Only when the browser did not already have one, so an existing id is never
    // rotated: a rotated id would double-count one person in the funnel.
    res.headers.append("set-cookie", boxVidSetCookie(vid, isHttps(req)));
  }
  return res;
}

/** True when the original client request arrived over https (nginx sets the header). */
function isHttps(req: Request): boolean {
  const proto = req.headers.get("x-forwarded-proto");
  if (proto) return proto.split(",")[0].trim() === "https";
  return new URL(req.url).protocol === "https:";
}

/**
 * Forwards one event to the durable store. Returns whether it landed, and never
 * throws: the door must keep working when the backend is down. The visitor's
 * request does not wait long for it either — a 4s ceiling, because the form is
 * blocking on this response.
 */
async function storeEvent(payload: Record<string, unknown>, clientAddr: string): Promise<boolean> {
  try {
    const upstream = await fetch(`${BACKEND_ORIGIN}/api/v1/box/leads`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        // Pass the visitor's address through, so the backend's per-IP bucket sees
        // the visitor rather than this pod (every event would otherwise share one
        // bucket and a busy landing would rate-limit itself). The backend's global
        // bucket is what guards against a forged value.
        "X-Forwarded-For": clientAddr,
      },
      body: JSON.stringify(payload),
      cache: "no-store",
      signal: AbortSignal.timeout(4000),
    });
    return upstream.ok;
  } catch {
    return false;
  }
}

async function notifyWebhook(record: Record<string, unknown>): Promise<void> {
  const url = process.env.BOX_LEADS_WEBHOOK_URL;
  if (!url) return;
  try {
    await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(record),
      signal: AbortSignal.timeout(5000),
    });
  } catch {
    // Never fail the user's request because our own notification hop is down.
  }
}

async function notifyTelegram(record: Record<string, unknown>): Promise<void> {
  const token = process.env.BOX_LEADS_TELEGRAM_TOKEN;
  const chat = process.env.BOX_LEADS_TELEGRAM_CHAT;
  if (!token || !chat) return;

  // page_view and demo_run are high-volume and low-signal — store them, but don't
  // page a human.
  if (QUIET_EVENTS.has(String(record.event))) return;

  const lines = Object.entries(record)
    .filter(([, v]) => v !== undefined && v !== null && v !== "")
    .map(([k, v]) => `${k}: ${Array.isArray(v) ? v.join("; ") : String(v)}`);

  try {
    await fetch(`https://api.telegram.org/bot${token}/sendMessage`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        chat_id: chat,
        text: `🧪 Dada Box — ${String(record.event)}\n\n${lines.join("\n")}`,
        disable_web_page_preview: true,
      }),
      signal: AbortSignal.timeout(5000),
    });
  } catch {
    // Same: notification failure must stay invisible to the person on the page.
  }
}

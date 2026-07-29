import { NextResponse } from "next/server";

/**
 * Funnel sink for the Dada Box private-preview fake door (/box, /en/box).
 *
 * Deliberately has no database and no backend dependency: the experiment must
 * ship without a platform release (docs/product/box-product-brief.md §8). Events
 * are written as one structured log line each, and optionally forwarded to a
 * webhook or Telegram chat so a human sees requests in real time and can
 * provision a box by hand (concierge mode).
 *
 * When Box graduates from experiment to product, replace this with a real
 * backend endpoint and a table.
 *
 * Env:
 *   BOX_LEADS_WEBHOOK_URL      generic JSON POST target (optional)
 *   BOX_LEADS_TELEGRAM_TOKEN   bot token (optional, needs CHAT too)
 *   BOX_LEADS_TELEGRAM_CHAT    chat id to notify (optional)
 */

export const dynamic = "force-dynamic";

const KNOWN_EVENTS = new Set(["demo_run", "box_requested", "crystallize_intent"]);

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

  const record: Record<string, unknown> = {
    at: new Date().toISOString(),
    event,
    locale: str(body.locale, 8) ?? "ru",
    referer: str(req.headers.get("referer") ?? undefined),
    ua: str(req.headers.get("user-agent") ?? undefined, 300),
  };

  let claim: string | undefined;

  if (event === "box_requested") {
    const email = str(body.email, 200);
    const useCase = str(body.useCase, MAX_USE_CASE);
    if (!email || !useCase) {
      return NextResponse.json({ error: "missing_fields" }, { status: 400 });
    }
    claim = claimCode();
    Object.assign(record, {
      claim,
      email,
      useCase,
      contact: str(body.contact),
      agent: str(body.agent, 60),
      parallel: str(body.parallel, 60),
      price: str(body.price, 60),
    });
  }

  if (event === "crystallize_intent") {
    const wants = Array.isArray(body.wants)
      ? body.wants.filter((w): w is string => typeof w === "string").slice(0, 20).map((w) => w.slice(0, MAX_FIELD))
      : [];
    Object.assign(record, { claim: str(body.claim, 40), wants });
  }

  // The log line IS the storage for this experiment. Keep it single-line JSON so
  // it survives log aggregation and can be grepped by event name.
  console.log(`box_funnel ${JSON.stringify(record)}`);

  await Promise.allSettled([notifyWebhook(record), notifyTelegram(record)]);

  return NextResponse.json(claim ? { ok: true, claim } : { ok: true });
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

  // demo_run is high-volume and low-signal — log it, but don't page a human.
  if (record.event === "demo_run") return;

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

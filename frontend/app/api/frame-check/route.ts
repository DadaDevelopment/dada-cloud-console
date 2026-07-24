import { NextRequest, NextResponse } from "next/server";

const ALLOWED_HOST_SUFFIX = ".dada-tuda.ru";
const FETCH_TIMEOUT_MS = 5000;

/**
 * True when the given X-Frame-Options value forbids cross-origin framing.
 * Console and preview apps live on different subdomains, so both `deny` and
 * `sameorigin` count as blocking here.
 */
function xfoBlocks(value: string | null): boolean {
  if (!value) return false;
  const v = value.trim().toLowerCase();
  return v === "deny" || v === "sameorigin";
}

/**
 * True when a Content-Security-Policy's frame-ancestors directive forbids
 * framing outright (`'none'`) or restricts to origins that cannot include the
 * console (bare `'self'` with nothing else).
 */
function cspBlocks(value: string | null): boolean {
  if (!value) return false;
  const match = /frame-ancestors\s+([^;]+)/i.exec(value);
  if (!match) return false;
  const sources = match[1].trim().toLowerCase().split(/\s+/);
  if (sources.length === 0) return false;
  if (sources.includes("*")) return false;
  if (sources.includes("'none'")) return true;
  return sources.every((s) => s === "'self'");
}

function isAllowedTarget(url: URL): boolean {
  if (url.protocol !== "https:") return false;
  return url.hostname.toLowerCase().endsWith(ALLOWED_HOST_SUFFIX);
}

export async function GET(request: NextRequest) {
  const raw = request.nextUrl.searchParams.get("url");
  if (!raw) {
    return NextResponse.json({ error: "missing url" }, { status: 400 });
  }

  let target: URL;
  try {
    target = new URL(raw);
  } catch {
    return NextResponse.json({ error: "invalid url" }, { status: 400 });
  }

  if (!isAllowedTarget(target)) {
    return NextResponse.json({ error: "host not allowed" }, { status: 400 });
  }

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), FETCH_TIMEOUT_MS);
  try {
    const res = await fetch(target.toString(), {
      method: "HEAD",
      redirect: "follow",
      signal: controller.signal,
      credentials: "omit",
      headers: { "User-Agent": "dada-cloud-frame-check" },
    });
    const xfo = res.headers.get("x-frame-options");
    const csp = res.headers.get("content-security-policy");
    const embeddable = !xfoBlocks(xfo) && !cspBlocks(csp);
    return NextResponse.json({ embeddable, status: res.status });
  } catch {
    return NextResponse.json({ embeddable: false, status: 0 });
  } finally {
    clearTimeout(timer);
  }
}

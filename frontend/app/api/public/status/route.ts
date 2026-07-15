import { NextResponse } from "next/server";

/**
 * Server-side proxy for the public competitor-availability probe.
 *
 * The marketing host (cloud.dada-tuda.ru) routes /api/* to the frontend, so a
 * client-side fetch to the backend's /api/public/status 404s here. The console
 * host routes /api/* straight to the backend, where this handler is shadowed by
 * ingress. This handler lets the /status page fetch same-origin on both hosts.
 */

const BACKEND_ORIGIN = (
  process.env.STATUS_BACKEND_ORIGIN ??
  process.env.NEXT_PUBLIC_CONSOLE_URL ??
  "https://console.dada-tuda.ru"
).replace(/\/$/, "");

export const dynamic = "force-dynamic";

export async function GET() {
  try {
    const upstream = await fetch(`${BACKEND_ORIGIN}/api/public/status`, {
      cache: "no-store",
      signal: AbortSignal.timeout(8000),
    });
    if (!upstream.ok) {
      return NextResponse.json(
        { error: "upstream", status: upstream.status },
        { status: 502 },
      );
    }
    const data = await upstream.json();
    return NextResponse.json(data, {
      headers: {
        "Cache-Control": "public, max-age=30, s-maxage=30",
        "Access-Control-Allow-Origin": "*",
      },
    });
  } catch {
    return NextResponse.json({ error: "unreachable" }, { status: 502 });
  }
}

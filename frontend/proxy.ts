import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";

// One frontend image serves BOTH the marketing host (cloud.dada-tuda.ru) and the
// console host (console.dada-tuda.ru). The marketing landing lives at "/", but on
// the console host "/" must keep redirecting into the app (/projects) — that was
// the old app/page.tsx behaviour, restored here per-host.
//
// MARKETING_HOST is a runtime env (proxy runs on the Node runtime, not baked).
// Empty in local dev so the landing is reachable at "/".
const MARKETING_HOST = process.env.MARKETING_HOST ?? "";

export function proxy(request: NextRequest) {
  const host = request.nextUrl.hostname;
  const isMarketingHost = MARKETING_HOST === "" || host === MARKETING_HOST;
  if (!isMarketingHost) {
    return NextResponse.redirect(new URL("/projects", request.url));
  }
  return NextResponse.next();
}

export const config = {
  matcher: "/",
};

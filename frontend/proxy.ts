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

// The real public host is in the proxy headers, NOT request.nextUrl.hostname:
// in the standalone server Next binds to (and reports) the pod hostname, so
// nextUrl.hostname is the pod name — never the external host. nginx ingress
// forwards the client's host as X-Forwarded-Host (and preserves Host), so read
// that. Strip any port. Fall back to nextUrl.hostname for local dev.
function publicHost(request: NextRequest): string {
  const header =
    request.headers.get("x-forwarded-host") ?? request.headers.get("host") ?? "";
  const host = header.split(",")[0].trim().split(":")[0];
  return host || request.nextUrl.hostname;
}

export function proxy(request: NextRequest) {
  const host = publicHost(request);
  const path = request.nextUrl.pathname;
  const isMarketingHost = MARKETING_HOST === "" || host === MARKETING_HOST;

  if (!isMarketingHost) {
    // Console host: only the root used to redirect into the app. Keep that and
    // leave every other console route alone (redirecting all paths would loop
    // /projects → /projects).
    if (path === "/") {
      return NextResponse.redirect(new URL("/projects", request.url));
    }
    return NextResponse.next();
  }

  // Marketing host: expose the URL-derived locale to server components (root
  // layout reads it to set <html lang> on SSR). "/en" + "/en/..." is English,
  // everything else is the RU default.
  const locale = path === "/en" || path.startsWith("/en/") ? "en" : "ru";
  const headers = new Headers(request.headers);
  headers.set("x-dada-locale", locale);
  return NextResponse.next({ request: { headers } });
}

export const config = {
  // Run on real pages, skip Next internals and static assets.
  matcher: ["/((?!_next/static|_next/image|favicon.ico|.*\\..*).*)"],
};

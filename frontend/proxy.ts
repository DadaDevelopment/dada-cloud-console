import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";
import {
  BOX_VID_COOKIE,
  boxVidSetCookie,
  isBoxLandingPath,
  isBoxVid,
  newBoxVid,
} from "@/lib/box-vid";

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
    // Console language is a cookie preference (no URL prefix). Surface it as the
    // same x-dada-locale header the root layout already reads for <html lang>,
    // so SSR emits the right language. Default RU when unset.
    const cookieLocale = request.cookies.get("dada_console_lang")?.value;
    const locale = cookieLocale === "en" ? "en" : "ru";
    const headers = new Headers(request.headers);
    headers.set("x-dada-locale", locale);
    headers.set("x-dada-path", path);
    return NextResponse.next({ request: { headers } });
  }

  // Marketing host: expose the URL-derived locale to server components (root
  // layout reads it to set <html lang> on SSR). "/en" + "/en/..." is English,
  // everything else is the RU default.
  const locale = path === "/en" || path.startsWith("/en/") ? "en" : "ru";
  const headers = new Headers(request.headers);
  headers.set("x-dada-locale", locale);
  headers.set("x-dada-path", path);
  const response = NextResponse.next({ request: { headers } });

  // Issue the anonymous Box visitor id on the first hit of the landing, so every
  // funnel event from this browser carries the same opaque key and the page-view
  // denominator can count people instead of reloads (lib/box-vid.ts).
  //
  // Issued here rather than from the page: the cookie is HttpOnly, and the id must
  // exist before the landing's first page_view fires. Only on /box and /en/box —
  // the rest of the marketing site has no funnel to attribute, and an id set on
  // every page would be tracking without a purpose.
  if (isBoxLandingPath(path) && !isBoxVid(request.cookies.get(BOX_VID_COOKIE)?.value)) {
    const proto = request.headers.get("x-forwarded-proto") ?? request.nextUrl.protocol.replace(":", "");
    response.headers.append("set-cookie", boxVidSetCookie(newBoxVid(), proto === "https"));
  }

  return response;
}

export const config = {
  // Run on real pages, skip Next internals and static assets.
  matcher: ["/((?!_next/static|_next/image|favicon.ico|.*\\..*).*)"],
};

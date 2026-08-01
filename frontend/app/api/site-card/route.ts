import { NextRequest, NextResponse } from "next/server";
import type { SiteCard } from "@/lib/site-card";

const ALLOWED_HOST_SUFFIX = ".dada-tuda.ru";
const FETCH_TIMEOUT_MS = 5000;
const MAX_HTML_BYTES = 256 * 1024;

function isAllowedTarget(url: URL): boolean {
  if (url.protocol !== "https:") return false;
  return url.hostname.toLowerCase().endsWith(ALLOWED_HOST_SUFFIX);
}

/**
 * Reads at most `MAX_HTML_BYTES` of the response body. A deployed app can serve
 * a document of any size; the card only needs `<head>`, so the stream is cut
 * short instead of buffering whatever the app decided to send.
 */
async function readCapped(res: Response): Promise<string> {
  const body = res.body;
  if (!body) return "";
  const reader = body.getReader();
  const decoder = new TextDecoder("utf-8");
  const chunks: string[] = [];
  let total = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      total += value.byteLength;
      chunks.push(decoder.decode(value, { stream: true }));
      if (total >= MAX_HTML_BYTES) break;
    }
  } finally {
    await reader.cancel().catch(() => {});
  }
  return chunks.join("");
}

function decodeEntities(text: string): string {
  return text
    .replace(/&quot;/g, '"')
    .replace(/&#0?39;/g, "'")
    .replace(/&apos;/g, "'")
    .replace(/&nbsp;/g, " ")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&amp;/g, "&")
    .replace(/\s+/g, " ")
    .trim();
}

/**
 * Value of the first `<meta>` whose `property`/`name` matches, tolerating any
 * attribute order. Hand-rolled rather than DOM-parsed: only a handful of tags
 * matter and pulling a parser in for them is not worth the weight. `keys` are
 * internal constants, never user input, so they go into the pattern as-is.
 */
function metaContent(html: string, keys: string[]): string | undefined {
  for (const key of keys) {
    const re = new RegExp(`<meta[^>]+(?:property|name)\\s*=\\s*["']${key}["'][^>]*>`, "i");
    const tag = re.exec(html)?.[0];
    if (!tag) continue;
    const content = /content\s*=\s*["']([^"']*)["']/i.exec(tag)?.[1];
    if (content && content.trim() !== "") return decodeEntities(content);
  }
  return undefined;
}

function documentTitle(html: string): string | undefined {
  const raw = /<title[^>]*>([\s\S]*?)<\/title>/i.exec(html)?.[1];
  if (!raw) return undefined;
  const text = decodeEntities(raw);
  return text === "" ? undefined : text;
}

/**
 * Absolute https URL for a card image, or `undefined`. Relative paths resolve
 * against the page, and the result must stay inside the platform's own domain:
 * the browser loads this URL directly, and an app should not be able to point
 * the console at an arbitrary third-party host.
 */
function cardImage(raw: string | undefined, base: URL): string | undefined {
  if (!raw) return undefined;
  try {
    const resolved = new URL(raw, base);
    return isAllowedTarget(resolved) ? resolved.toString() : undefined;
  } catch {
    return undefined;
  }
}

function clamp(text: string | undefined, max: number): string | undefined {
  if (!text) return undefined;
  return text.length > max ? `${text.slice(0, max - 1).trimEnd()}…` : text;
}

/**
 * Open Graph card for a deployed app: the same summary a chat client shows when
 * the app's URL is pasted. The console renders it as an instant stand-in for the
 * live preview, which costs a full page load of the app in an iframe.
 *
 * Same guards as the frame-check route: https only, platform hosts only, hard
 * timeout — the server must not be talked into fetching arbitrary addresses.
 */
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
      method: "GET",
      redirect: "follow",
      signal: controller.signal,
      credentials: "omit",
      headers: {
        "User-Agent": "dada-cloud-site-card",
        Accept: "text/html,application/xhtml+xml",
      },
    });

    const contentType = res.headers.get("content-type") ?? "";
    if (!res.ok || !contentType.toLowerCase().includes("html")) {
      return NextResponse.json({ url: target.toString(), status: res.status } satisfies SiteCard);
    }

    const html = await readCapped(res);
    const card: SiteCard = {
      url: target.toString(),
      status: res.status,
      title: clamp(metaContent(html, ["og:title", "twitter:title"]) ?? documentTitle(html), 120),
      description: clamp(metaContent(html, ["og:description", "twitter:description", "description"]), 220),
      image: cardImage(metaContent(html, ["og:image", "twitter:image", "og:image:url"]), target),
      siteName: clamp(metaContent(html, ["og:site_name"]), 60),
    };
    return NextResponse.json(card);
  } catch {
    return NextResponse.json({ url: target.toString(), status: 0 } satisfies SiteCard);
  } finally {
    clearTimeout(timer);
  }
}

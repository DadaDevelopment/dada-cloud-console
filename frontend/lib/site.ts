// Marketing landing and the console are served from the SAME frontend image but
// on two hosts (cloud.dada-tuda.ru = marketing, console.dada-tuda.ru = console).
// Auth/console links always target the console host so OIDC login uses the
// whitelisted redirect URI. NEXT_PUBLIC_CONSOLE_URL is baked at build time:
//   - set to https://console.dada-tuda.ru → absolute links (cross-origin from
//     the marketing host, same-origin from the console host)
//   - empty (local dev) → relative links, so the local server handles them.
const CONSOLE_URL = (process.env.NEXT_PUBLIC_CONSOLE_URL ?? "").replace(/\/$/, "");

export function consoleHref(path: string): string {
  if (!CONSOLE_URL) return path;
  return `${CONSOLE_URL}${path}`;
}

// Public user guides live on the marketing host under /developer/<slug>. The
// console (a different origin) links to them via absolute URLs. There is no
// dedicated marketing-origin env today, so this falls back to the production
// marketing host; override with NEXT_PUBLIC_MARKETING_URL if that ever changes.
const MARKETING_URL = (process.env.NEXT_PUBLIC_MARKETING_URL ?? "https://cloud.dada-tuda.ru").replace(/\/$/, "");

export function docsHref(slug: string): string {
  return `${MARKETING_URL}/developer/${slug}`;
}

// Marketing locale routing: RU is served at the root ("/", "/pricing"), EN at
// the "/en" prefix ("/en", "/en/pricing"). Keep internal marketing links inside
// the active locale so navigation doesn't bounce the user back to RU.
// Pass a root-relative path ("/", "/pricing", "/#how"); console/external links
// must keep using consoleHref instead.
export function localeHref(path: string, locale: "ru" | "en"): string {
  if (locale !== "en") return path;
  if (path === "/") return "/en";
  if (path.startsWith("/#")) return `/en${path.slice(1)}`; // "/#how" -> "/en#how"
  if (path.startsWith("/")) return `/en${path}`;
  return path; // anchors, external, already-prefixed
}

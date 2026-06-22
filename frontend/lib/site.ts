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

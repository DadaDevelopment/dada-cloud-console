const CONSOLE_PATH_PREFIXES = ["/projects", "/admin", "/ai-studio", "/billing", "/deploy"];

const SEGMENT = "[A-Za-z0-9_\\-~%](?:[A-Za-z0-9_\\-.~%]*[A-Za-z0-9_\\-~%])?";

const PATH_BODY = "/(?:projects|admin|ai-studio|billing|deploy)(?:/" + SEGMENT + ")*";

const FENCE = /```[\s\S]*?```/g;
const LINK = /\[[^\]]*\]\([^)]*\)/g;

const CODE_PATH = new RegExp("`(" + PATH_BODY + ")`", "g");
const BARE_PATH = new RegExp(
  "(^|[\\s(\\u00ab\"'>])(" + PATH_BODY + ")(?=$|[\\s)\\u00bb\"'<,;!?]|\\.(?:\\s|$))",
  "g",
);

/**
 * True when a href points at a page this console renders itself, so the panel
 * can hand it to the router instead of letting the browser reload the app.
 */
export function isInternalConsolePath(href: string): boolean {
  if (!href || !href.startsWith("/") || href.startsWith("//")) return false;
  const path = href.split(/[?#]/)[0];
  return CONSOLE_PATH_PREFIXES.some(
    (prefix) => path === prefix || path.startsWith(prefix + "/"),
  );
}

/**
 * Every console route the app actually renders, as segment patterns where a
 * `[name]` segment matches any single path segment.
 *
 * Mirrors the `page.tsx` files under `app/(console)` plus the standalone
 * `/deploy` page; `agent-chat-links.test.ts` walks the filesystem and fails if
 * the two drift. It exists so a deep link suggested by the assistant can be
 * checked against reality instead of a prefix guess - `/billing` and
 * `/projects/{id}/apps/{app}/logs` look plausible and are both dead.
 */
export const CONSOLE_ROUTES: readonly string[] = [
  "/admin",
  "/admin/ai-gateway",
  "/admin/approvals",
  "/admin/audit",
  "/admin/costs",
  "/admin/db-shards",
  "/admin/feedback",
  "/admin/funnel",
  "/ai-studio",
  "/billing/return",
  "/deploy",
  "/projects",
  "/projects/[projectId]",
  "/projects/[projectId]/agents",
  "/projects/[projectId]/ai",
  "/projects/[projectId]/app-servers",
  "/projects/[projectId]/app-servers/[serverName]",
  "/projects/[projectId]/apps",
  "/projects/[projectId]/apps/[appName]",
  "/projects/[projectId]/apps/[appName]/builds/[buildId]",
  "/projects/[projectId]/apps/[appName]/compose",
  "/projects/[projectId]/apps/[appName]/deployments",
  "/projects/[projectId]/apps/[appName]/files",
  "/projects/[projectId]/apps/[appName]/settings",
  "/projects/[projectId]/apps/[appName]/values",
  "/projects/[projectId]/billing",
  "/projects/[projectId]/boxes",
  "/projects/[projectId]/databases",
  "/projects/[projectId]/databases/[name]",
  "/projects/[projectId]/databases/[name]/tables/[table]",
  "/projects/[projectId]/domains",
  "/projects/[projectId]/git",
  "/projects/[projectId]/git/import",
  "/projects/[projectId]/members",
  "/projects/[projectId]/models",
  "/projects/[projectId]/models/[name]",
  "/projects/[projectId]/monitoring",
  "/projects/[projectId]/monitoring/[appId]",
  "/projects/[projectId]/operations",
  "/projects/[projectId]/storage",
  "/projects/[projectId]/storage/[name]",
];

function segmentsOf(path: string): string[] {
  return path.split("/").filter(Boolean);
}

function routeMatches(pattern: string, path: string): boolean {
  const p = segmentsOf(pattern);
  const s = segmentsOf(path);
  if (p.length !== s.length) return false;
  return p.every((seg, i) => (seg.startsWith("[") && seg.endsWith("]") ? s[i].length > 0 : seg === s[i]));
}

/**
 * Finds the `CONSOLE_ROUTES` pattern a href resolves to, or null when none
 * matches. Query and hash are ignored: a hash targets an anchor on an
 * existing page, which is a valid deep link.
 *
 * The returned pattern is the `[projectId]`-style template, never the real
 * path -- callers that need to log a navigation without naming the resource
 * it landed on (telemetry) use this instead of the href itself.
 */
export function matchConsoleRouteTemplate(href: string): string | null {
  if (!href || !href.startsWith("/") || href.startsWith("//")) return null;
  const path = href.split(/[?#]/)[0];
  if (path === "/") return null;
  return CONSOLE_ROUTES.find((pattern) => routeMatches(pattern, path)) ?? null;
}

/**
 * True when href resolves to a page that exists. Query and hash are ignored:
 * a hash targets an anchor on an existing page, which is a valid deep link.
 */
export function isKnownConsoleRoute(href: string): boolean {
  return matchConsoleRouteTemplate(href) !== null;
}

const MD_LINK_HREF = /(\[[^\]]*\]\()([^)\s]+)(\))/g;

/**
 * Repairs a console path the model wrote by hand, or returns null when it is
 * already a real route or cannot be salvaged.
 *
 * The model names paths in prose and gets the shape wrong in two specific
 * ways, both observed on production: it drops the leading slash
 * (`projects/{id}/git/import`), which makes the browser resolve the link
 * relative to the page the user is already on, and it writes the collection
 * segment in the singular (`project/{id}/...`). Either one is a 404 for a user
 * who was told this is where their deploy continues.
 *
 * Only rewrites that land on a route in `CONSOLE_ROUTES` are applied, so a
 * genuinely external or unknown href is left exactly as written.
 */
export function repairConsoleHref(href: string): string | null {
  if (!href || isKnownConsoleRoute(href)) return null;
  if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith("//") || href.startsWith("#")) return null;

  const candidates: string[] = [];
  const slashed = href.startsWith("/") ? href : "/" + href;
  candidates.push(slashed);
  if (slashed.startsWith("/project/")) candidates.push("/projects/" + slashed.slice("/project/".length));

  return candidates.find((candidate) => isKnownConsoleRoute(candidate)) ?? null;
}

/**
 * Rewrites the hrefs of markdown links the assistant produced so a link that
 * points at a real console page actually reaches it. Link text is untouched:
 * the user reads what the model wrote and lands where it meant.
 */
export function repairConsoleLinks(markdown: string): string {
  if (!markdown) return markdown;
  MD_LINK_HREF.lastIndex = 0;
  return markdown.replace(MD_LINK_HREF, (match, head: string, href: string, tail: string) => {
    const repaired = repairConsoleHref(href);
    return repaired === null ? match : head + repaired + tail;
  });
}

type Span = { start: number; end: number };

function spansOf(text: string, patterns: RegExp[]): Span[] {
  const spans: Span[] = [];
  for (const re of patterns) {
    re.lastIndex = 0;
    let m: RegExpExecArray | null;
    while ((m = re.exec(text)) !== null) {
      spans.push({ start: m.index, end: m.index + m[0].length });
    }
  }
  return spans;
}

function inside(spans: Span[], start: number, end: number): boolean {
  return spans.some((s) => start < s.end && end > s.start);
}

/**
 * Turns console routes in an assistant reply into markdown links.
 *
 * The model reliably names a path but rarely writes markdown around it, and an
 * unlinked path is the same dead end as "press the create button". Both bare
 * paths and paths wrapped in inline backticks are converted; the backticks are
 * kept inside the link label so the rendered anchor stays monospace. Paths in a
 * fenced code block, or already inside a markdown link, are left untouched.
 */
export function autolinkConsolePaths(markdown: string): string {
  if (!markdown) return markdown;

  const first = spansOf(markdown, [FENCE, LINK]);
  CODE_PATH.lastIndex = 0;
  const withCode = markdown.replace(CODE_PATH, (match, path: string, offset: number) => {
    if (inside(first, offset, offset + match.length)) return match;
    return "[`" + path + "`](" + path + ")";
  });

  const second = spansOf(withCode, [FENCE, LINK]);
  BARE_PATH.lastIndex = 0;
  return withCode.replace(BARE_PATH, (match, lead: string, path: string, offset: number) => {
    const start = offset + lead.length;
    if (inside(second, start, start + path.length)) return match;
    return lead + "[`" + path + "`](" + path + ")";
  });
}

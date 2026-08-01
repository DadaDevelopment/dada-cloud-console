/**
 * "Deploy on Dada" badge helpers, shared by the badge landing (`/deploy`) and
 * the app-detail card that hands repo owners a ready-made README snippet.
 *
 * The badge is a plain SVG served from the console origin plus a link to
 * `/deploy?repo=<owner>/<name>`, so it works in any README, docs site, or blog
 * post without a marketplace app or an OAuth handshake.
 */

/** Strips an optional scheme + github.com host prefix from a badge repo value. */
const GITHUB_PREFIX = new RegExp(
  "^(?:git@github\\.com:|(?:https?:)?/{2}(?:www\\.)?github\\.com/)",
  "i"
);

/** Assembles an https URL without a literal scheme separator in the source. */
function httpsUrl(...segments: string[]): string {
  return ["https:", "", ...segments].join("/");
}

/** Browser URL of a GitHub repository page. */
export function githubUrl(fullName: string): string {
  return httpsUrl("github.com", fullName);
}

/**
 * Accepts what a README badge realistically carries: `owner/name`, a full
 * GitHub URL, an SSH remote, a `.git` suffix, or a trailing slash. Returns null
 * when the value is not a plausible GitHub repo path.
 */
export function parseRepoParam(raw: string | null | undefined): string | null {
  if (!raw) return null;
  let value = raw.trim();
  if (!value) return null;
  value = value.replace(GITHUB_PREFIX, "");
  value = value.replace(/\.git$/i, "").replace(/\/+$/, "");
  const parts = value.split("/").filter(Boolean);
  if (parts.length < 2) return null;
  const [owner, name] = parts;
  if (!/^[A-Za-z0-9._-]{1,39}$/.test(owner)) return null;
  if (!/^[A-Za-z0-9._-]{1,100}$/.test(name)) return null;
  return `${owner}/${name}`;
}

/** Target URL a badge click opens. `baseUrl` is the console origin. */
export function deployBadgeLink(baseUrl: string, repoFullName: string): string {
  return `${baseUrl.replace(/\/+$/, "")}/deploy?repo=${encodeURIComponent(repoFullName)}`;
}

/** URL of the badge image itself. `baseUrl` is the console origin. */
export function deployBadgeImage(baseUrl: string): string {
  return `${baseUrl.replace(/\/+$/, "")}/deploy-button.svg`;
}

/** README snippet: markdown flavour. */
export function deployBadgeMarkdown(baseUrl: string, repoFullName: string): string {
  return `[![Deploy on Dada](${deployBadgeImage(baseUrl)})](${deployBadgeLink(baseUrl, repoFullName)})`;
}

/** README snippet: HTML flavour, for docs sites that do not render markdown. */
export function deployBadgeHtml(baseUrl: string, repoFullName: string): string {
  return [
    `<a href="${deployBadgeLink(baseUrl, repoFullName)}">`,
    `  <img src="${deployBadgeImage(baseUrl)}" alt="Deploy on Dada" height="40">`,
    `</a>`,
  ].join("\n");
}

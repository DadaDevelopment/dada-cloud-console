import fs from "node:fs";
import path from "node:path";

const DOCS_DIR = path.join(process.cwd(), "content/docs");
const SLUG_RE = /^[a-z0-9-]+$/i;

function isArticle(file: string): boolean {
  return file.endsWith(".md") && file.toLowerCase() !== "readme.md";
}

/**
 * Slugs (filename without `.md`) of every published guide, derived from the
 * `content/docs` directory listing. `README.md` is the index intro, not an
 * article, so it is excluded.
 */
export function getDocSlugs(): string[] {
  return fs
    .readdirSync(DOCS_DIR)
    .filter(isArticle)
    .map((file) => file.replace(/\.md$/, ""))
    .sort();
}

/**
 * Raw Markdown for a guide, or `null` when the slug is unknown or unsafe. The
 * slug is validated against a strict allowlist to prevent path traversal.
 */
export function getDocMarkdown(slug: string): string | null {
  if (!SLUG_RE.test(slug) || slug.toLowerCase() === "readme") return null;
  const file = path.join(DOCS_DIR, `${slug}.md`);
  if (path.dirname(file) !== DOCS_DIR || !fs.existsSync(file)) return null;
  return fs.readFileSync(file, "utf8");
}

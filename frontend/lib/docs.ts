import fs from "node:fs";
import path from "node:path";
import type { Metadata } from "next";

const DOCS_DIR = path.join(process.cwd(), "content/docs");
const SLUG_RE = /^[a-z0-9-]+$/i;
const SITE_URL = "https://cloud.dada-tuda.ru";

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

/**
 * The public marketing surface never names the orchestration layer. Guide bodies
 * are technical and may reference it; anything lifted from a guide into a page
 * title, meta description or JSON-LD node is scrubbed of that vocabulary so the
 * machine-readable surface stays consistent with the visible copy.
 */
function stripInfraJargon(text: string): string {
  return text
    .replace(/\bKubernetes\/KServe\b/gi, "")
    .replace(/\bKubernetes-native\b/gi, "container-native")
    .replace(/\bthe Kubernetes resource name\b/gi, "the resource name")
    .replace(/\bKubernetes\b/gi, "")
    .replace(/\bKServe\b/gi, "")
    .replace(/\bkubeconfig\b/gi, "")
    .replace(/\bk8s\b/gi, "")
    .replace(/\s+([,.;:])/g, "$1")
    .replace(/\(\s+/g, "(")
    .replace(/\s{2,}/g, " ")
    .trim();
}

/**
 * Collapse a Markdown fragment to a single line of plain, quotable prose:
 * inline code, emphasis and links become their text, whitespace is normalised.
 */
function toPlainText(text: string): string {
  return text
    .replace(/`([^`]+)`/g, "$1")
    .replace(/\*\*([^*]+)\*\*/g, "$1")
    .replace(/\*([^*]+)\*/g, "$1")
    .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
    .replace(/\s+/g, " ")
    .trim();
}

function truncate(text: string, max: number): string {
  if (text.length <= max) return text;
  const cut = text.slice(0, max);
  const lastSpace = cut.lastIndexOf(" ");
  return `${cut.slice(0, lastSpace > 40 ? lastSpace : max).replace(/[\s,.;:—-]+$/, "")}…`;
}

function bodyAfterHeading(markdown: string, headingRe: RegExp): string[] {
  const lines = markdown.split(/\r?\n/);
  const start = lines.findIndex((l) => headingRe.test(l));
  if (start < 0) return [];
  const out: string[] = [];
  for (let i = start + 1; i < lines.length; i++) {
    if (/^##\s/.test(lines[i])) break;
    out.push(lines[i]);
  }
  return out;
}

/** First `# H1`, scrubbed to plain text. Falls back to a generic label. */
export function getDocTitle(markdown: string): string {
  const m = markdown.match(/^#\s+(.+)$/m);
  return m ? stripInfraJargon(toPlainText(m[1])) : "Guide";
}

/**
 * A ~155-char meta description derived from the guide's `## What it's for`
 * section (its purpose statement), so every guide gets a unique, on-topic snippet.
 */
export function getDocSummary(markdown: string): string {
  const body = bodyAfterHeading(markdown, /^##\s+What it's for/i)
    .filter((l) => !/^\s*>/.test(l))
    .map((l) => l.replace(/^\s*([-*+]|\d+\.)\s+/, ""))
    .join(" ");
  const plain = stripInfraJargon(toPlainText(body));
  return truncate(plain, 155);
}

/**
 * Ordered steps for HowTo structured data, drawn from either a `## How …`
 * section's numbered list or a sequence of `## Step N — …` headings. Returns an
 * empty array when the guide has no step sequence, so only genuine how-tos emit
 * HowTo JSON-LD.
 */
export function getDocSteps(markdown: string): Array<{ name: string; text: string }> {
  const lines = markdown.split(/\r?\n/);

  const stepHeadings = lines.filter((l) => /^##\s+Step\s+\d+/i.test(l));
  if (stepHeadings.length >= 2) {
    return stepHeadings
      .map((heading) => {
        const title = heading.replace(/^##\s+Step\s+\d+\s*[—-]\s*/i, "").trim();
        const first = bodyAfterHeading(markdown, new RegExp(`^${escapeRe(heading)}`))
          .map((l) => l.replace(/^\s*([-*+]|\d+\.)\s+/, "").trim())
          .find((l) => l.length > 0) ?? title;
        return {
          name: stripInfraJargon(toPlainText(title)),
          text: stripInfraJargon(toPlainText(first)),
        };
      })
      .filter((s) => s.name.length > 0);
  }

  const howIdx = lines.findIndex((l) => /^##\s+How\b/i.test(l));
  if (howIdx < 0) return [];
  const steps: Array<{ name: string; text: string }> = [];
  let current: string | null = null;
  let frozen = false;
  for (let i = howIdx + 1; i < lines.length; i++) {
    const line = lines[i];
    if (/^##\s/.test(line)) break;
    const numbered = line.match(/^(\d+)\.\s+(.*)$/);
    if (numbered) {
      if (current !== null) steps.push(finishStep(current));
      current = numbered[2];
      frozen = false;
    } else if (current !== null && /^\s{2,}[-*+]\s/.test(line)) {
      frozen = true;
    } else if (current !== null && !frozen && /^\s{1,3}\S/.test(line) && line.trim() !== "") {
      current += ` ${line.trim()}`;
    }
  }
  if (current !== null) steps.push(finishStep(current));
  return steps.length >= 2 ? steps : [];
}

function escapeRe(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function finishStep(raw: string): { name: string; text: string } {
  const text = stripInfraJargon(toPlainText(raw));
  const clause = text.split(/[.:—]/)[0].trim();
  const name = truncate(clause.length >= 4 ? clause : text, 72);
  return { name, text };
}

/**
 * Full per-guide `Metadata` for a `/developer/<slug>` article: a unique title and
 * description mined from the guide, self-canonical for the given locale, with
 * ru/en hreflang alternates and Open Graph / Twitter cards.
 */
export function docMetadata(slug: string, locale: "ru" | "en"): Metadata {
  const markdown = getDocMarkdown(slug);
  if (markdown === null) return {};
  const title = getDocTitle(markdown);
  const description = getDocSummary(markdown);
  const ruUrl = `${SITE_URL}/developer/${slug}`;
  const enUrl = `${SITE_URL}/en/developer/${slug}`;
  const url = locale === "en" ? enUrl : ruUrl;

  return {
    title,
    description,
    alternates: {
      canonical: locale === "en" ? `/en/developer/${slug}` : `/developer/${slug}`,
      languages: {
        "ru-RU": `/developer/${slug}`,
        "en-US": `/en/developer/${slug}`,
        "x-default": `/developer/${slug}`,
      },
    },
    openGraph: {
      type: "article",
      url,
      siteName: "DADA Cloud",
      title,
      description,
      locale: locale === "en" ? "en_US" : "ru_RU",
      images: [{ url: "/og.png", width: 1200, height: 630, alt: title }],
    },
    twitter: {
      card: "summary_large_image",
      title,
      description,
      images: ["/og.png"],
    },
  };
}

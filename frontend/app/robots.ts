import type { MetadataRoute } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";

// AI / LLM crawlers are allowed explicitly (GEO/AEO): some respect only their
// own named block, so the allowlist is spelled out alongside the wildcard rule.
const AI_CRAWLERS = [
  "GPTBot",
  "OAI-SearchBot",
  "ChatGPT-User",
  "ClaudeBot",
  "Claude-Web",
  "anthropic-ai",
  "PerplexityBot",
  "Perplexity-User",
  "Google-Extended",
  "Applebot",
  "Applebot-Extended",
  "Amazonbot",
  "Meta-ExternalAgent",
  "FacebookBot",
  "Bytespider",
  "CCBot",
  "cohere-ai",
  "DuckAssistBot",
  "YandexAdditional",
];

export default function robots(): MetadataRoute.Robots {
  // Console surfaces live on console.dada-tuda.ru, but guard the paths here too
  // in case they are ever reachable on the marketing host.
  const disallow = ["/projects", "/admin", "/ai-studio", "/callback", "/login"];
  return {
    rules: [
      { userAgent: "*", allow: "/", disallow },
      ...AI_CRAWLERS.map((userAgent) => ({ userAgent, allow: "/" })),
    ],
    sitemap: `${SITE_URL}/sitemap.xml`,
    host: SITE_URL,
  };
}

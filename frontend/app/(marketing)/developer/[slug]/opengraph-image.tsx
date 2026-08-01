import { notFound } from "next/navigation";
import { getDocMarkdown, getDocSlugs, getDocTitle, getDocSummary } from "@/lib/docs";
import { ogCard, OG_SIZE, OG_CONTENT_TYPE } from "@/lib/og-card";

export const alt = "DADA Cloud";
export const size = OG_SIZE;
export const contentType = OG_CONTENT_TYPE;

export function generateStaticParams() {
  return getDocSlugs().map((slug) => ({ slug }));
}

/**
 * Per-article card, so a doc link shared in a chat shows the article's own
 * headline instead of the landing page's banner.
 */
export default async function Image({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  const markdown = getDocMarkdown(slug, "ru");
  if (markdown === null) notFound();
  return ogCard({
    eyebrow: "Документация",
    title: getDocTitle(markdown),
    subtitle: getDocSummary(markdown),
  });
}

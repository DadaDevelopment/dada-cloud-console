import type { Metadata } from "next";
import { getDocSlugs, docMetadata } from "@/lib/docs";
import { DocArticle } from "@/components/marketing/doc-article";

export function generateStaticParams() {
  return getDocSlugs().map((slug) => ({ slug }));
}

export async function generateMetadata({ params }: { params: Promise<{ slug: string }> }): Promise<Metadata> {
  const { slug } = await params;
  return docMetadata(slug, "en");
}

export default async function EnDocArticlePage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  return <DocArticle slug={slug} locale="en" />;
}

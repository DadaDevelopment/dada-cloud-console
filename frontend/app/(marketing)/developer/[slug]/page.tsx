import type { Metadata } from "next";
import { notFound } from "next/navigation";
import {
  getDocMarkdown,
  getDocSlugs,
  getDocTitle,
  getDocSummary,
  getDocSteps,
  docMetadata,
} from "@/lib/docs";
import { renderMarkdown } from "@/lib/markdown";
import { DocBackLink, DocLangNote } from "@/components/marketing/doc-chrome";
import { DocJsonLd } from "@/components/marketing/doc-jsonld";

export function generateStaticParams() {
  return getDocSlugs().map((slug) => ({ slug }));
}

export async function generateMetadata({ params }: { params: Promise<{ slug: string }> }): Promise<Metadata> {
  const { slug } = await params;
  return docMetadata(slug, "ru");
}

export default async function DocArticlePage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  const markdown = getDocMarkdown(slug);
  if (markdown === null) notFound();
  const html = renderMarkdown(markdown);

  return (
    <section className="bg-white py-16">
      <div className="mx-auto max-w-3xl px-4 sm:px-6 lg:px-8">
        <DocJsonLd
          title={getDocTitle(markdown)}
          description={getDocSummary(markdown)}
          steps={getDocSteps(markdown)}
        />
        <div className="mb-6">
          <DocBackLink />
        </div>
        <DocLangNote />
        <div className="dada-doc" dangerouslySetInnerHTML={{ __html: html }} />
        <div className="mt-12 border-t border-slate-200 pt-6">
          <DocBackLink />
        </div>
      </div>
    </section>
  );
}

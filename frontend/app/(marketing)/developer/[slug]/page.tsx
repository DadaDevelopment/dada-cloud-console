import { notFound } from "next/navigation";
import { getDocMarkdown, getDocSlugs } from "@/lib/docs";
import { renderMarkdown } from "@/lib/markdown";
import { DocBackLink, DocLangNote } from "@/components/marketing/doc-chrome";

export function generateStaticParams() {
  return getDocSlugs().map((slug) => ({ slug }));
}

export default async function DocArticlePage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  const markdown = getDocMarkdown(slug);
  if (markdown === null) notFound();
  const html = renderMarkdown(markdown);

  return (
    <section className="bg-white py-16">
      <div className="mx-auto max-w-3xl px-4 sm:px-6 lg:px-8">
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

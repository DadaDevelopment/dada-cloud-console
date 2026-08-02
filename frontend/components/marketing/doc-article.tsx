import { notFound } from "next/navigation";
import type { DocLocale } from "@/lib/docs";
import { getDocMarkdown, getDocTitle, getDocSummary, getDocSteps, isDocTranslated } from "@/lib/docs";
import { renderMarkdown } from "@/lib/markdown";
import { DocBackLink, DocLangNote } from "@/components/marketing/doc-chrome";
import { DocJsonLd } from "@/components/marketing/doc-jsonld";

/**
 * One guide article. Both `/developer/<slug>` and `/en/developer/<slug>` render
 * this; the locale decides which Markdown body is served, so a RU reader gets
 * the translation when it exists and the English original (with a note) when it
 * does not.
 */
export function DocArticle({ slug, locale }: { slug: string; locale: DocLocale }) {
  const markdown = getDocMarkdown(slug, locale);
  if (markdown === null) notFound();
  const html = renderMarkdown(markdown);
  const untranslated = locale === "ru" && !isDocTranslated(slug);

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
        {untranslated && <DocLangNote />}
        <div className="dada-doc" dangerouslySetInnerHTML={{ __html: html }} />
        <div className="mt-12 border-t border-slate-200 pt-6">
          <DocBackLink />
        </div>
      </div>
    </section>
  );
}

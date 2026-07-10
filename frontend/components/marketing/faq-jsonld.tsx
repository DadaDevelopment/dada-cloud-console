"use client";

import { useLang } from "@/lib/i18n/context";

/**
 * FAQPage structured data for a marketing product page. Pass the same `items`
 * that render in the visible FAQ so the rich result mirrors the page, plus the
 * root-relative `path` of the RU page (e.g. "/databases"); the locale-aware
 * self URL is derived from the active locale. Answers are used verbatim, so keep
 * them as concise, self-contained sentences an answer engine can quote.
 */
const SITE_URL = "https://cloud.dada-tuda.ru";

export function FaqJsonLd({
  path,
  items,
}: {
  path: string;
  items: { q: string; a: string }[];
}) {
  const { locale } = useLang();
  const pageUrl = `${SITE_URL}${locale === "en" ? "/en" : ""}${path}`;

  const data = {
    "@context": "https://schema.org",
    "@type": "FAQPage",
    "@id": `${pageUrl}#faq`,
    inLanguage: locale,
    mainEntity: items.map(({ q, a }) => ({
      "@type": "Question",
      name: q,
      acceptedAnswer: { "@type": "Answer", text: a },
    })),
  };

  return (
    <script
      type="application/ld+json"
      dangerouslySetInnerHTML={{ __html: JSON.stringify(data) }}
    />
  );
}

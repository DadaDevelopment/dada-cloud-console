"use client";

import { useLang } from "@/lib/i18n/context";

/**
 * HowTo structured data for a step-by-step marketing guide page. Pass the
 * root-relative `path` of the RU page (e.g. "/migrate-vercel"); the
 * locale-aware self URL is derived from the active locale, same convention as
 * FaqJsonLd. Steps are used verbatim, so keep them concise and self-contained.
 */
const SITE_URL = "https://cloud.dada-tuda.ru";

export function HowToJsonLd({
  path,
  name,
  description,
  steps,
}: {
  path: string;
  name: string;
  description: string;
  steps: { name: string; text: string }[];
}) {
  const { locale } = useLang();
  const pageUrl = `${SITE_URL}${locale === "en" ? "/en" : ""}${path}`;

  const data = {
    "@context": "https://schema.org",
    "@type": "HowTo",
    "@id": `${pageUrl}#howto`,
    name,
    description,
    inLanguage: locale,
    step: steps.map((s, i) => ({
      "@type": "HowToStep",
      position: i + 1,
      name: s.name,
      text: s.text,
      url: `${pageUrl}#step-${String(i + 1).padStart(2, "0")}`,
    })),
  };

  return (
    <script
      type="application/ld+json"
      dangerouslySetInnerHTML={{ __html: JSON.stringify(data) }}
    />
  );
}

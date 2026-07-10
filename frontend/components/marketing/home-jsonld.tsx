"use client";

/**
 * FAQPage structured data for the marketing home page, built from the same
 * `t.home.faq` copy that renders visibly on the page, so the rich result mirrors
 * the page exactly. The Organization, WebSite and SoftwareApplication nodes are
 * emitted site-wide by SiteJsonLd (from the layout); this component only carries
 * the home FAQ so the entity graph is not duplicated. Locale follows the
 * rendered page (RU at "/", EN at "/en").
 */

import { useLang } from "@/lib/i18n/context";

const SITE_URL = "https://cloud.dada-tuda.ru";

export function HomeJsonLd() {
  const { t, locale } = useLang();
  const pageUrl = locale === "en" ? `${SITE_URL}/en` : SITE_URL;

  const faqPage = {
    "@context": "https://schema.org",
    "@type": "FAQPage",
    "@id": `${pageUrl}/#faq`,
    inLanguage: locale,
    mainEntity: t.home.faq.map(({ q, a }) => ({
      "@type": "Question",
      name: q,
      acceptedAnswer: { "@type": "Answer", text: a },
    })),
  };

  return (
    <script
      type="application/ld+json"
      dangerouslySetInnerHTML={{ __html: JSON.stringify(faqPage) }}
    />
  );
}

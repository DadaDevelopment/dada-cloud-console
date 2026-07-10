"use client";

import { usePathname } from "next/navigation";

/**
 * Structured data for a single `/developer/<slug>` guide. The server page mines
 * the title, description and ordered steps from the guide Markdown and passes
 * them in; this client component derives the locale + slug from the URL and
 * emits a BreadcrumbList (Home → Developer → guide), a TechArticle for every
 * guide, and a HowTo when the guide is a genuine step-by-step (2+ steps).
 */
const SITE_URL = "https://cloud.dada-tuda.ru";

export function DocJsonLd({
  title,
  description,
  steps,
}: {
  title: string;
  description: string;
  steps: { name: string; text: string }[];
}) {
  const pathname = usePathname() ?? "/";
  const isEn = pathname === "/en" || pathname.startsWith("/en/");
  const locale = isEn ? "en" : "ru";
  const slug = pathname.replace(/^\/en/, "").replace(/^\/developer\//, "").replace(/\/$/, "");

  const prefix = isEn ? `${SITE_URL}/en` : SITE_URL;
  const pageUrl = `${prefix}/developer/${slug}`;
  const developerName = isEn ? "Developer" : "Для разработчиков";
  const homeName = isEn ? "Home" : "Главная";

  const breadcrumb = {
    "@context": "https://schema.org",
    "@type": "BreadcrumbList",
    itemListElement: [
      { "@type": "ListItem", position: 1, name: homeName, item: `${prefix}/` },
      { "@type": "ListItem", position: 2, name: developerName, item: `${prefix}/developer` },
      { "@type": "ListItem", position: 3, name: title, item: pageUrl },
    ],
  };

  const article = {
    "@context": "https://schema.org",
    "@type": "TechArticle",
    "@id": `${pageUrl}#article`,
    headline: title,
    description,
    inLanguage: locale,
    url: pageUrl,
    isPartOf: { "@id": `${SITE_URL}/#website` },
    publisher: { "@id": `${SITE_URL}/#organization` },
  };

  const graphs: object[] = [breadcrumb, article];

  if (steps.length >= 2) {
    graphs.push({
      "@context": "https://schema.org",
      "@type": "HowTo",
      "@id": `${pageUrl}#howto`,
      name: title,
      description,
      inLanguage: locale,
      step: steps.map((s, i) => ({
        "@type": "HowToStep",
        position: i + 1,
        name: s.name,
        text: s.text,
        url: `${pageUrl}#step-${i + 1}`,
      })),
    });
  }

  return (
    <>
      {graphs.map((graph, i) => (
        <script
          key={i}
          type="application/ld+json"
          dangerouslySetInnerHTML={{ __html: JSON.stringify(graph) }}
        />
      ))}
    </>
  );
}

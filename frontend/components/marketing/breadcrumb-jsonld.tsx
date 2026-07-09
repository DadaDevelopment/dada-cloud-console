"use client";

import { usePathname } from "next/navigation";

// BreadcrumbList (Schema.org / JSON-LD) for inner marketing pages, mirroring the
// breadcrumb feature on development.dada-tuda.ru. Derived from the pathname so a
// single instance in the marketing layout covers every RU and EN inner page.
const SITE_URL = "https://cloud.dada-tuda.ru";

const LABELS: Record<string, { ru: string; en: string }> = {
  "cloud-servers": { ru: "Серверы", en: "Servers" },
  kubernetes: { ru: "Kubernetes", en: "Kubernetes" },
  databases: { ru: "Базы данных", en: "Databases" },
  storage: { ru: "Объектное хранилище", en: "Object storage" },
  pricing: { ru: "Цены", en: "Pricing" },
  developer: { ru: "Для разработчиков", en: "Developer" },
};

export function BreadcrumbJsonLd() {
  const pathname = usePathname() ?? "/";
  const isEn = pathname === "/en" || pathname.startsWith("/en/");
  const lang = isEn ? "en" : "ru";

  const slug = pathname.replace(/^\/en/, "").replace(/^\//, "").replace(/\/$/, "");
  if (!slug || !LABELS[slug]) return null;

  const homeUrl = isEn ? `${SITE_URL}/en` : `${SITE_URL}/`;
  const pageUrl = isEn ? `${SITE_URL}/en/${slug}` : `${SITE_URL}/${slug}`;

  const data = {
    "@context": "https://schema.org",
    "@type": "BreadcrumbList",
    itemListElement: [
      { "@type": "ListItem", position: 1, name: isEn ? "Home" : "Главная", item: homeUrl },
      { "@type": "ListItem", position: 2, name: LABELS[slug][lang], item: pageUrl },
    ],
  };

  return (
    <script
      // eslint-disable-next-line react/no-danger
      type="application/ld+json"
      dangerouslySetInnerHTML={{ __html: JSON.stringify(data) }}
    />
  );
}

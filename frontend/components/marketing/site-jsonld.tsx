"use client";

import { usePathname } from "next/navigation";

/**
 * Site-wide structured data emitted on every marketing route from the layout:
 * the Organization, the WebSite (with a SearchAction pointing at the guide
 * index) and the SoftwareApplication product node. Locale is derived from the
 * URL ("/en" → English) so `inLanguage` and the descriptions match the rendered
 * page. Page-specific graphs (FAQPage, BreadcrumbList, HowTo) live in their own
 * components; these three anchor the brand entity across the whole site.
 */
const SITE_URL = "https://cloud.dada-tuda.ru";

const COPY = {
  ru: {
    url: SITE_URL,
    orgDesc:
      "Бэкенд-облако: подключи GitHub-репозиторий и за минуты получи рабочий бэкенд с Postgres, доменом, HTTPS и откатом в один клик — без своей DevOps-команды.",
    appDesc:
      "PaaS-облако для бэкенда: деплой из GitHub одним push, управляемый PostgreSQL с автоматическим DATABASE_URL, домены и HTTPS, мониторинг и откат. Свой VPS подключается по SSH и ведётся из той же панели — альтернатива Heroku, Vercel и self-hosted Coolify.",
    priceCurrency: "RUB",
    offerDesc: "Бесплатный старт, оплата по мере роста.",
  },
  en: {
    url: `${SITE_URL}/en`,
    orgDesc:
      "Backend cloud: connect a GitHub repo and get a working backend in minutes — Postgres, a domain, HTTPS and one-click rollback, with no DevOps team of your own.",
    appDesc:
      "PaaS backend cloud: deploy from GitHub with one push, managed PostgreSQL with an automatic DATABASE_URL, domains and HTTPS, monitoring and rollback. Bring your own VPS over SSH and run it from the same panel — an alternative to Heroku, Vercel and self-hosted Coolify.",
    priceCurrency: "USD",
    offerDesc: "Free to start, pay as you grow.",
  },
} as const;

export function SiteJsonLd() {
  const pathname = usePathname() ?? "/";
  const locale = pathname === "/en" || pathname.startsWith("/en/") ? "en" : "ru";
  const c = COPY[locale];

  const organization = {
    "@context": "https://schema.org",
    "@type": "Organization",
    "@id": `${SITE_URL}/#organization`,
    name: "DADA Cloud",
    url: SITE_URL,
    logo: `${SITE_URL}/og.png`,
    description: c.orgDesc,
    parentOrganization: {
      "@type": "Organization",
      "@id": "https://development.dada-tuda.ru/#organization",
      name: "DADA Development",
      url: "https://development.dada-tuda.ru/",
    },
    sameAs: [
      "https://development.dada-tuda.ru/",
      "https://a2a-hub.pro/",
      "https://github.com/DadaDevelopment/dada-cloud-console",
    ],
  };

  const website = {
    "@context": "https://schema.org",
    "@type": "WebSite",
    "@id": `${SITE_URL}/#website`,
    name: "DADA Cloud",
    url: c.url,
    inLanguage: locale,
    publisher: { "@id": `${SITE_URL}/#organization` },
    potentialAction: {
      "@type": "SearchAction",
      target: {
        "@type": "EntryPoint",
        urlTemplate: `${SITE_URL}${locale === "en" ? "/en" : ""}/developer?q={search_term_string}`,
      },
      "query-input": "required name=search_term_string",
    },
  };

  const softwareApplication = {
    "@context": "https://schema.org",
    "@type": "SoftwareApplication",
    "@id": `${SITE_URL}/#software`,
    name: "DADA Cloud",
    url: c.url,
    applicationCategory: "DeveloperApplication",
    operatingSystem: "Web",
    description: c.appDesc,
    author: {
      "@type": "Organization",
      "@id": "https://development.dada-tuda.ru/#organization",
      name: "DADA Development",
      url: "https://development.dada-tuda.ru/",
    },
    publisher: { "@id": `${SITE_URL}/#organization` },
    offers: {
      "@type": "Offer",
      priceCurrency: c.priceCurrency,
      price: "0",
      description: c.offerDesc,
    },
  };

  const graphs = [organization, website, softwareApplication];

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

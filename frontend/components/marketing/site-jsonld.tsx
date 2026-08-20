"use client";

import { usePathname } from "next/navigation";

import { COMPANY } from "@/lib/company";

/**
 * Site-wide structured data emitted on every marketing route from the layout:
 * the Organization, the WebSite (with a SearchAction), a SiteNavigationElement
 * ItemList (main sections — the strongest on-page signal we control for organic
 * sitelinks) and the SoftwareApplication product node (with an AggregateOffer
 * carrying the real published tiers so the offer graph mirrors /pricing). Locale
 * is derived from the URL ("/en" → English) so `inLanguage`, descriptions and
 * currency match the rendered page. Page-specific graphs (FAQPage,
 * BreadcrumbList, HowTo) live in their own components; these anchor the brand
 * entity across the whole site.
 */
const SITE_URL = "https://cloud.dada-tuda.ru";

type PlanOffer = { name: string; price: string; desc: string };

const COPY = {
  ru: {
    url: SITE_URL,
    prefix: "",
    orgDesc:
      "Бэкенд-облако: подключи GitHub-репозиторий и за минуты получи рабочий бэкенд с Postgres, доменом, HTTPS и откатом в один клик — без своей DevOps-команды.",
    appDesc:
      "PaaS-облако для бэкенда: деплой из GitHub одним push, управляемый PostgreSQL с автоматическим DATABASE_URL, домены и HTTPS, мониторинг и откат. Свой VPS подключается по SSH и ведётся из той же панели — альтернатива Heroku, Vercel и self-hosted Coolify.",
    priceCurrency: "RUB",
    nav: [
      { name: "Как это работает", path: "/#how" },
      { name: "Серверы", path: "/cloud-servers" },
      { name: "Базы данных", path: "/databases" },
      { name: "Хранилище", path: "/storage" },
      { name: "Цены", path: "/pricing" },
      { name: "Документация", path: "/developer" },
    ],
    lowPrice: "0",
    highPrice: "2900",
    plans: [
      { name: "Free", price: "0", desc: "Пет-проекты: 1 приложение, 1 база данных, 2 ГБ, 1 домен." },
      { name: "Startup", price: "990", desc: "Один разработчик в продакшене: 5 приложений, 2 базы, бэкапы 7 дней." },
      { name: "Business", price: "2900", desc: "Команда с продакшеном: 20 приложений, 10 баз, бэкапы 30 дней." },
    ] as PlanOffer[],
    featureList: [
      "Деплой из GitHub одним push",
      "Управляемый PostgreSQL с автоматическим DATABASE_URL",
      "Домены и бесплатный HTTPS",
      "Откат в один клик",
      "Мониторинг и логи",
      "Свой VPS по SSH из той же панели",
    ],
  },
  en: {
    url: `${SITE_URL}/en`,
    prefix: "/en",
    orgDesc:
      "Backend cloud: connect a GitHub repo and get a working backend in minutes — Postgres, a domain, HTTPS and one-click rollback, with no DevOps team of your own.",
    appDesc:
      "PaaS backend cloud: deploy from GitHub with one push, managed PostgreSQL with an automatic DATABASE_URL, domains and HTTPS, monitoring and rollback. Bring your own VPS over SSH and run it from the same panel — an alternative to Heroku, Vercel and self-hosted Coolify.",
    priceCurrency: "USD",
    nav: [
      { name: "How it works", path: "/#how" },
      { name: "Servers", path: "/cloud-servers" },
      { name: "Databases", path: "/databases" },
      { name: "Storage", path: "/storage" },
      { name: "Pricing", path: "/pricing" },
      { name: "Docs", path: "/developer" },
    ],
    lowPrice: "0",
    highPrice: "35",
    plans: [
      { name: "Free", price: "0", desc: "Pet projects: 2 applications, 1 database, 2 GB, 1 domain." },
      { name: "Startup", price: "12", desc: "Solo developer in production: 5 applications, 2 databases, 7-day backups." },
      { name: "Business", price: "35", desc: "Growing team in production: 20 applications, 10 databases, 30-day backups." },
    ] as PlanOffer[],
    featureList: [
      "Deploy from GitHub with one push",
      "Managed PostgreSQL with an automatic DATABASE_URL",
      "Domains and free HTTPS",
      "One-click rollback",
      "Monitoring and logs",
      "Bring your own VPS over SSH from the same panel",
    ],
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
    legalName: COMPANY.fullName,
    taxID: COMPANY.inn,
    vatID: COMPANY.inn,
    address: {
      "@type": "PostalAddress",
      addressCountry: "RU",
      addressLocality: "Санкт-Петербург",
      postalCode: "198335",
      streetAddress: COMPANY.legalAddress,
    },
    contactPoint: {
      "@type": "ContactPoint",
      contactType: "customer support",
      email: COMPANY.email,
      availableLanguage: ["ru", "en"],
    },
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

  const navigation = {
    "@context": "https://schema.org",
    "@type": "ItemList",
    "@id": `${SITE_URL}/#nav`,
    name: "DADA Cloud",
    itemListElement: c.nav.map((item, i) => ({
      "@type": "SiteNavigationElement",
      position: i + 1,
      name: item.name,
      url: `${SITE_URL}${c.prefix}${item.path}`,
    })),
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
    featureList: c.featureList,
    author: {
      "@type": "Organization",
      "@id": "https://development.dada-tuda.ru/#organization",
      name: "DADA Development",
      url: "https://development.dada-tuda.ru/",
    },
    publisher: { "@id": `${SITE_URL}/#organization` },
    offers: {
      "@type": "AggregateOffer",
      priceCurrency: c.priceCurrency,
      lowPrice: c.lowPrice,
      highPrice: c.highPrice,
      offerCount: c.plans.length,
      url: `${SITE_URL}${c.prefix}/pricing`,
      offers: c.plans.map((p) => ({
        "@type": "Offer",
        name: p.name,
        price: p.price,
        priceCurrency: c.priceCurrency,
        description: p.desc,
        url: `${SITE_URL}${c.prefix}/pricing`,
      })),
    },
  };

  const graphs = [organization, website, navigation, softwareApplication];

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

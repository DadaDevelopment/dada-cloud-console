"use client";

// Structured data (Schema.org / JSON-LD) for the marketing home page.
// Strings are hard-coded here on purpose (kept out of the i18n dictionary) and
// mirror the public positioning: a backend cloud that ships your backend from
// GitHub in minutes. Locale follows the rendered page (RU at "/", EN at "/en")
// so the FAQ rich result matches the visible language. Keep wording in sync
// with the visible home copy when it changes.
//
// Three graphs are emitted: Organization, SoftwareApplication, FAQPage.

import { useLang } from "@/lib/i18n/context";

const SITE_URL = "https://cloud.dada-tuda.ru";

type Copy = {
  pageUrl: string;
  orgDesc: string;
  appDesc: string;
  priceCurrency: string;
  offerDesc: string;
  faq: Array<{ q: string; a: string }>;
};

const COPY: Record<"ru" | "en", Copy> = {
  ru: {
    pageUrl: SITE_URL,
    orgDesc:
      "Бэкенд-облако: подключи GitHub-репозиторий и за минуты получи рабочий бэкенд с Postgres, доменом, HTTPS и откатом в один клик.",
    appDesc:
      "Бэкенд-облако для основателей, стартапов и агентств: деплой бэкенда из GitHub за минуты, управляемый Postgres, домены, HTTPS и откат в один клик — без DevOps.",
    priceCurrency: "RUB",
    offerDesc: "Бесплатный старт, оплата по мере роста.",
    faq: [
      {
        q: "Нужно ли мне знать Kubernetes или DevOps?",
        a: "Нет. Вы подключаете GitHub-репозиторий, а DADA Cloud сам собирает, деплоит и обслуживает бэкенд. Сложный Kubernetes скрыт за простым интерфейсом.",
      },
      {
        q: "Как быстро бэкенд окажется онлайн?",
        a: "За минуты. После подключения репозитория платформа собирает приложение, поднимает Postgres, выдаёт домен и HTTPS — без ручной настройки серверов.",
      },
      {
        q: "Что будет с базой данных?",
        a: "Вы получаете управляемый Postgres из коробки: резервные копии и подключение настраиваются автоматически вместе с деплоем бэкенда.",
      },
      {
        q: "Что если деплой сломает прод?",
        a: "Откат в один клик. Каждый деплой можно безопасно вернуть к предыдущей рабочей версии.",
      },
      {
        q: "Кому подходит DADA Cloud?",
        a: "Соло-основателям, небольшим стартапам (2–10 человек) и агентствам, которым нужен рабочий бэкенд без своей DevOps-команды.",
      },
    ],
  },
  en: {
    pageUrl: `${SITE_URL}/en`,
    orgDesc:
      "Backend cloud: connect a GitHub repo and get a working backend in minutes — Postgres, a domain, HTTPS and one-click rollback.",
    appDesc:
      "Backend cloud for founders, startups and agencies: deploy your backend from GitHub in minutes, managed Postgres, domains, HTTPS and one-click rollback — without DevOps.",
    priceCurrency: "USD",
    offerDesc: "Free to start, pay as you grow.",
    faq: [
      {
        q: "Do I need to know Kubernetes or DevOps?",
        a: "No. You connect a GitHub repo and DADA Cloud builds, deploys and runs the backend for you. The Kubernetes complexity stays hidden behind a simple interface.",
      },
      {
        q: "How fast is the backend online?",
        a: "In minutes. Once the repo is connected the platform builds the app, provisions Postgres, issues a domain and HTTPS — no manual server setup.",
      },
      {
        q: "What about the database?",
        a: "You get managed Postgres out of the box: backups and connection are wired up automatically alongside the backend deploy.",
      },
      {
        q: "What if a deploy breaks production?",
        a: "One-click rollback. Every deploy can be safely reverted to the previous working version.",
      },
      {
        q: "Who is DADA Cloud for?",
        a: "Solo founders, small startups (2–10 people) and agencies that need a working backend without their own DevOps team.",
      },
    ],
  },
};

export function HomeJsonLd() {
  const { locale } = useLang();
  const c = COPY[locale];

  const organization = {
    "@context": "https://schema.org",
    "@type": "Organization",
    "@id": `${SITE_URL}/#organization`,
    name: "DADA Cloud",
    url: SITE_URL,
    logo: `${SITE_URL}/og.png`,
    description: c.orgDesc,
  };

  const softwareApplication = {
    "@context": "https://schema.org",
    "@type": "SoftwareApplication",
    "@id": `${SITE_URL}/#software`,
    name: "DADA Cloud",
    url: c.pageUrl,
    applicationCategory: "DeveloperApplication",
    operatingSystem: "Web",
    description: c.appDesc,
    publisher: { "@id": `${SITE_URL}/#organization` },
    offers: {
      "@type": "Offer",
      priceCurrency: c.priceCurrency,
      price: "0",
      description: c.offerDesc,
    },
  };

  const faqPage = {
    "@context": "https://schema.org",
    "@type": "FAQPage",
    "@id": `${c.pageUrl}/#faq`,
    mainEntity: c.faq.map(({ q, a }) => ({
      "@type": "Question",
      name: q,
      acceptedAnswer: { "@type": "Answer", text: a },
    })),
  };

  const graphs = [organization, softwareApplication, faqPage];

  return (
    <>
      {graphs.map((graph, i) => (
        <script
          // eslint-disable-next-line react/no-danger
          key={i}
          type="application/ld+json"
          dangerouslySetInnerHTML={{ __html: JSON.stringify(graph) }}
        />
      ))}
    </>
  );
}

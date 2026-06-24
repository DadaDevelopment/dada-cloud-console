// Structured data (Schema.org / JSON-LD) for the marketing home page.
// Strings are hard-coded here on purpose (kept out of the i18n dictionary) and
// mirror the public positioning: a backend cloud that ships your backend from
// GitHub in minutes. Update in sync with the visible home copy when it changes.
//
// Three graphs are emitted:
//   - Organization      → brand entity for knowledge panel / sitelinks
//   - SoftwareApplication → the product itself (developer / cloud tooling)
//   - FAQPage           → mirrors the home FAQ for rich results

const SITE_URL = "https://cloud.dada-tuda.ru";

const organization = {
  "@context": "https://schema.org",
  "@type": "Organization",
  "@id": `${SITE_URL}/#organization`,
  name: "DADA Cloud",
  url: SITE_URL,
  logo: `${SITE_URL}/og.png`,
  description:
    "Бэкенд-облако: подключи GitHub-репозиторий и за минуты получи рабочий бэкенд с Postgres, доменом, HTTPS и откатом в один клик.",
};

const softwareApplication = {
  "@context": "https://schema.org",
  "@type": "SoftwareApplication",
  "@id": `${SITE_URL}/#software`,
  name: "DADA Cloud",
  url: SITE_URL,
  applicationCategory: "DeveloperApplication",
  operatingSystem: "Web",
  description:
    "Бэкенд-облако для основателей, стартапов и агентств: деплой бэкенда из GitHub за минуты, управляемый Postgres, домены, HTTPS и откат в один клик — без DevOps.",
  publisher: { "@id": `${SITE_URL}/#organization` },
  offers: {
    "@type": "Offer",
    priceCurrency: "RUB",
    price: "0",
    description: "Бесплатный старт, оплата по мере роста.",
  },
};

// Mirrors the visible home FAQ (t.home.faq). Hard-coded RU to match the default
// locale rendered server-side; keep wording in sync with the FAQ section.
const FAQ: Array<{ q: string; a: string }> = [
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
];

const faqPage = {
  "@context": "https://schema.org",
  "@type": "FAQPage",
  "@id": `${SITE_URL}/#faq`,
  mainEntity: FAQ.map(({ q, a }) => ({
    "@type": "Question",
    name: q,
    acceptedAnswer: { "@type": "Answer", text: a },
  })),
};

const GRAPHS = [organization, softwareApplication, faqPage];

export function HomeJsonLd() {
  return (
    <>
      {GRAPHS.map((graph, i) => (
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

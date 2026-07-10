import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "API и руководства для разработчиков";
const DESCRIPTION =
  "REST API /api/v1 с авторизацией по токену и пошаговые руководства: деплой из GitHub, серверы (BYO-VM), управляемый Postgres, домены и HTTPS, мониторинг. Всё, что делает консоль, доступно через API.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  alternates: {
    canonical: "/developer",
    languages: {
      "ru-RU": "/developer",
      "en-US": "/en/developer",
      "x-default": "/developer",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/developer`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — API и руководства для разработчиков" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function DeveloperLayout({ children }: { children: React.ReactNode }) {
  return children;
}

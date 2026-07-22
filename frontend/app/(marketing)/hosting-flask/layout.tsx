import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Хостинг Flask без сервера и nginx - Dada Cloud";
const DESCRIPTION =
  "Задеплойте Flask-приложение без своего сервера: подключите репозиторий, платформа соберёт и запустит его с HTTPS-доменом. Упало - поднимется само. Бесплатный тариф, серверы в России.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "хостинг flask",
    "куда задеплоить flask",
    "бесплатный хостинг flask",
    "хостинг flask приложения",
    "деплой flask без docker",
  ],
  alternates: {
    canonical: "/hosting-flask",
    languages: {
      "ru-RU": "/hosting-flask",
      "en-US": "/en/hosting-flask",
      "x-default": "/hosting-flask",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/hosting-flask`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function Layout({ children }: { children: React.ReactNode }) {
  return children;
}

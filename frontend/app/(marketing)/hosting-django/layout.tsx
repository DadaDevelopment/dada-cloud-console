import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Хостинг Django без своего сервера - Dada Cloud";
const DESCRIPTION =
  "Задеплойте Django-проект без настройки сервера: платформа сама определит django, соберёт проект и запустит с PostgreSQL и HTTPS-доменом. Бесплатный тариф, серверы в России.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "хостинг django",
    "куда задеплоить django",
    "бесплатный хостинг django",
    "хостинг django проекта",
    "деплой django",
  ],
  alternates: {
    canonical: "/hosting-django",
    languages: {
      "ru-RU": "/hosting-django",
      "en-US": "/en/hosting-django",
      "x-default": "/hosting-django",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/hosting-django`,
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

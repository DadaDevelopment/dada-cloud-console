import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Хостинг FastAPI без Docker - Dada Cloud";
const DESCRIPTION =
  "Задеплойте FastAPI за минуты: подключите репозиторий, платформа сама определит фреймворк, соберёт и запустит приложение с HTTPS-доменом. Упало - поднимется само. Бесплатный тариф, серверы в России.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "хостинг fastapi",
    "куда задеплоить fastapi",
    "бесплатный хостинг fastapi",
    "хостинг python api",
    "деплой fastapi без docker",
  ],
  alternates: {
    canonical: "/hosting-fastapi",
    languages: {
      "ru-RU": "/hosting-fastapi",
      "en-US": "/en/hosting-fastapi",
      "x-default": "/hosting-fastapi",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/hosting-fastapi`,
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

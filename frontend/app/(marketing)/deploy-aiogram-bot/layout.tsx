import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Деплой бота на aiogram за пару минут | Dada Cloud";
const DESCRIPTION =
  "Подключите репозиторий с ботом на aiogram 3 - платформа соберёт его и запустит постоянным процессом. Long polling работает без домена, вебхук получает HTTPS-адрес. Бесплатный тариф, серверы в России.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "деплой aiogram",
    "хостинг бота на aiogram",
    "как задеплоить телеграм бота на aiogram",
    "aiogram 3 деплой",
    "бесплатный хостинг aiogram",
  ],
  alternates: {
    canonical: "/deploy-aiogram-bot",
    languages: {
      "ru-RU": "/deploy-aiogram-bot",
      "en-US": "/en/deploy-aiogram-bot",
      "x-default": "/deploy-aiogram-bot",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/deploy-aiogram-bot`,
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

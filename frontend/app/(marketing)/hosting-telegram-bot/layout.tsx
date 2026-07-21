import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Хостинг телеграм-бота без Docker — Dada Cloud";
const DESCRIPTION =
  "Задеплойте телеграм-бота за 30 секунд без Docker: подключите репозиторий, бот сам соберётся и запустится через webhook. Упал — поднимется сам. Бесплатный хостинг, серверы в России.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "хостинг телеграм бота",
    "куда задеплоить бота",
    "хостинг aiogram",
    "бесплатный хостинг бота",
    "хостинг для python телеграм бота",
  ],
  alternates: {
    canonical: "/hosting-telegram-bot",
    languages: {
      "ru-RU": "/hosting-telegram-bot",
      "en-US": "/en/hosting-telegram-bot",
      "x-default": "/hosting-telegram-bot",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/hosting-telegram-bot`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — хостинг телеграм-бота без Docker" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function TelegramBotHostingLayout({ children }: { children: React.ReactNode }) {
  return children;
}

import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Хостинг для Telegram-бота в России — Dada Cloud";
const DESCRIPTION =
  "Разместите Telegram-бота на серверах в России: деплой из GitHub, оплата рублёвой картой, без VPN и зарубежных карт. Бот работает круглосуточно, база данных для его состояния — рядом.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "хостинг telegram бота",
    "где разместить telegram бота",
    "деплой telegram бота из github",
    "хостинг бота в россии",
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
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — хостинг для Telegram-бота в России" }],
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

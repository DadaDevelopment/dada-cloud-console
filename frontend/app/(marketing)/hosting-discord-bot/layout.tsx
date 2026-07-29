import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Хостинг Discord-бота - запусти и забудь | Dada Cloud";
const DESCRIPTION =
  "Задеплойте бота на discord.py или discord.js за пару минут: подключите репозиторий, платформа соберёт и запустит его фоновым процессом. Домен не нужен, упал - поднимется сам. Бесплатный тариф, серверы в России.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "хостинг discord бота",
    "где разместить discord бота",
    "бесплатный хостинг discord бота",
    "деплой discord.py",
    "хостинг бота discord.js",
  ],
  alternates: {
    canonical: "/hosting-discord-bot",
    languages: {
      "ru-RU": "/hosting-discord-bot",
      "en-US": "/en/hosting-discord-bot",
      "x-default": "/hosting-discord-bot",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/hosting-discord-bot`,
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

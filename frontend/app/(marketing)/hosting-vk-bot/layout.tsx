import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Хостинг бота ВКонтакте - Dada Cloud";
const DESCRIPTION =
  "Задеплойте бота ВК за пару минут: платформа соберёт и запустит его, выдаст HTTPS-адрес для Callback API. Упал - поднимется сам. Бесплатный тариф, серверы в России.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "хостинг вк бота",
    "хостинг бота вконтакте",
    "куда задеплоить вк бота",
    "бесплатный хостинг вк бота",
    "callback api бот хостинг",
  ],
  alternates: {
    canonical: "/hosting-vk-bot",
    languages: {
      "ru-RU": "/hosting-vk-bot",
      "en-US": "/en/hosting-vk-bot",
      "x-default": "/hosting-vk-bot",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/hosting-vk-bot`,
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

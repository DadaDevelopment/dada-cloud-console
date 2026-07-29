import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Deploy an aiogram bot in minutes | Dada Cloud";
const DESCRIPTION =
  "Connect a repo with your aiogram 3 bot and the platform builds it and runs it as a permanent process. Long polling needs no domain, webhook mode gets an HTTPS address. Free tier, servers in Russia.";

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
    canonical: "/en/deploy-aiogram-bot",
    languages: {
      "ru-RU": "/deploy-aiogram-bot",
      "en-US": "/en/deploy-aiogram-bot",
      "x-default": "/deploy-aiogram-bot",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/deploy-aiogram-bot`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
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

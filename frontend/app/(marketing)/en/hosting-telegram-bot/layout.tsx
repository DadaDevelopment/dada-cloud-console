import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Telegram bot hosting in Russia — Dada Cloud";
const DESCRIPTION =
  "Host your Telegram bot on servers inside Russia: deploy from GitHub, pay with a Russian card, no VPN or foreign card needed. The bot runs around the clock, with a database for its state right alongside it.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "telegram bot hosting russia",
    "where to host telegram bot",
    "deploy telegram bot from github",
    "python telegram bot hosting",
  ],
  alternates: {
    canonical: "/en/hosting-telegram-bot",
    languages: {
      "ru-RU": "/hosting-telegram-bot",
      "en-US": "/en/hosting-telegram-bot",
      "x-default": "/hosting-telegram-bot",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/hosting-telegram-bot`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — Telegram bot hosting in Russia" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function EnTelegramBotHostingLayout({ children }: { children: React.ReactNode }) {
  return children;
}

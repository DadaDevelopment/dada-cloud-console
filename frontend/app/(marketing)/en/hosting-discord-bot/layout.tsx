import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Discord bot hosting - deploy it and forget it | Dada Cloud";
const DESCRIPTION =
  "Deploy a discord.py or discord.js bot in minutes: connect a repo and the platform builds and runs it as a background process. No domain needed, crashes restart themselves. Free tier, servers in Russia.";

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
    canonical: "/en/hosting-discord-bot",
    languages: {
      "ru-RU": "/hosting-discord-bot",
      "en-US": "/en/hosting-discord-bot",
      "x-default": "/hosting-discord-bot",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/hosting-discord-bot`,
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

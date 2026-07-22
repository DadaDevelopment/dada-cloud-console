import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "VK bot hosting - Dada Cloud";
const DESCRIPTION =
  "Deploy a VK bot in a couple of minutes: the platform builds and runs it and issues an HTTPS address for the Callback API. Crashes restart themselves. Free tier, servers in Russia.";

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
    canonical: "/en/hosting-vk-bot",
    languages: {
      "ru-RU": "/hosting-vk-bot",
      "en-US": "/en/hosting-vk-bot",
      "x-default": "/hosting-vk-bot",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/hosting-vk-bot`,
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

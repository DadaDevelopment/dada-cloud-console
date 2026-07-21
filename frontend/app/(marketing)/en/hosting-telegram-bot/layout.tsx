import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Telegram bot hosting without Docker — Dada Cloud";
const DESCRIPTION =
  "Deploy a Telegram bot in 30 seconds without Docker: connect the repo, the bot builds and runs over webhook automatically. Crashes restart on their own. Free hosting, servers in Russia.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "telegram bot hosting russia",
    "deploy telegram bot no docker",
    "aiogram hosting",
    "free telegram bot hosting",
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
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — Telegram bot hosting without Docker" }],
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

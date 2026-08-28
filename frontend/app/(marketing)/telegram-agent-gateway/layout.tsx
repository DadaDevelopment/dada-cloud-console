import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Как создать AI-бота для Telegram без кода | Dada Cloud";
const DESCRIPTION =
  "Пошаговая инструкция: опишите роль в промпте, получите токен у @BotFather, вставьте его в консоли Dada Cloud - и AI-агент начнёт отвечать в Telegram. Без кода, без вебхуков, без своего сервера под бота.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "как создать телеграм бота на ии",
    "ии бот телеграм без кода",
    "создать ai бота в telegram",
    "telegram бот для ai агента",
    "ai агент в telegram без кода",
    "long polling telegram без домена",
  ],
  alternates: {
    canonical: "/telegram-agent-gateway",
    languages: {
      "ru-RU": "/telegram-agent-gateway",
      "en-US": "/en/telegram-agent-gateway",
      "x-default": "/telegram-agent-gateway",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/telegram-agent-gateway`,
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

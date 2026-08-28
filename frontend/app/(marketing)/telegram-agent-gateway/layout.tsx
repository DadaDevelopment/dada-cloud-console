import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Подключить AI-агента к Telegram-боту | Dada Cloud";
const DESCRIPTION =
  "Вставьте токен бота в форме агента - Dada Cloud проверит его через Telegram getMe и запустит long polling без домена и вебхука. Агент, который уже работает на платформе, начнёт отвечать в Telegram за минуту.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "telegram бот для ai агента",
    "подключить бота к llm агенту",
    "telegram gateway для агента",
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

import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "MCP-сервер DADA Cloud: управление облаком из Claude - Dada Cloud";
const DESCRIPTION =
  "Подключите DADA Cloud к Claude Code, Claude Desktop или Cursor как MCP-сервер: 41 инструмент консоли - деплой, серверы, базы, домены, логи - прямо из чата. Вход через браузер по аккаунту DADA ID, токены в конфиге не нужны. Адрес: https://console.dada-tuda.ru/mcp.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "mcp сервер",
    "dada cloud mcp",
    "деплой из claude",
    "управление облаком из ai агента",
    "model context protocol хостинг",
    "claude code деплой приложения",
  ],
  alternates: {
    canonical: "/mcp",
    languages: {
      "ru-RU": "/mcp",
      "en-US": "/en/mcp",
      "x-default": "/mcp",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/mcp`,
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

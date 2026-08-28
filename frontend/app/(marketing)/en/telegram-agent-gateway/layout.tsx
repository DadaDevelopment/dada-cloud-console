import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Connect an AI Agent to Telegram | Dada Cloud";
const DESCRIPTION =
  "Paste a bot token into the agent's form - Dada Cloud validates it against Telegram's getMe and starts long polling with no domain or webhook setup. An agent already running on the platform starts answering in Telegram within a minute.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "telegram bot for ai agent",
    "connect bot to llm agent",
    "telegram gateway for agent",
    "ai agent in telegram no code",
    "telegram long polling no domain",
  ],
  alternates: {
    canonical: "/en/telegram-agent-gateway",
    languages: {
      "ru-RU": "/telegram-agent-gateway",
      "en-US": "/en/telegram-agent-gateway",
      "x-default": "/telegram-agent-gateway",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/telegram-agent-gateway`,
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

export default function EnTelegramAgentGatewayLayout({ children }: { children: React.ReactNode }) {
  return children;
}

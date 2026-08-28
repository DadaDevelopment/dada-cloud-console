import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "How to Build an AI Telegram Bot With No Code | Dada Cloud";
const DESCRIPTION =
  "Step by step: describe the bot's role in a prompt, get a token from @BotFather, paste it into the Dada Cloud console - and your AI agent starts answering in Telegram. No code, no webhooks, no server to run.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "how to build an ai telegram bot",
    "ai telegram bot no code",
    "create ai bot in telegram",
    "telegram bot for ai agent",
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

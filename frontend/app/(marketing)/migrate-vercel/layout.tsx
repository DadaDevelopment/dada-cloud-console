import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Переезд с Vercel на Dada Cloud - пошаговый гайд";
const DESCRIPTION =
  "Как перенести существующий проект с Vercel на Dada Cloud: подключение репозитория, перенос переменных окружения, домен с автоматическим TLS и честная таблица соответствий vercel.json - что переносится 1:1, а что нет.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "переезд с vercel",
    "миграция с vercel",
    "перенос проекта с vercel",
    "vercel не принимает карту",
    "vercel.json аналог",
    "деплой next.js в россии",
  ],
  alternates: {
    canonical: "/migrate-vercel",
    languages: {
      "ru-RU": "/migrate-vercel",
      "en-US": "/en/migrate-vercel",
      "x-default": "/migrate-vercel",
    },
  },
  openGraph: {
    type: "article",
    url: `${SITE_URL}/migrate-vercel`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud - переезд с Vercel" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function MigrateVercelLayout({ children }: { children: React.ReactNode }) {
  return children;
}

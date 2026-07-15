import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Vercel не работает в России? Проверка доступности Vercel, Railway, Render из РФ";
const DESCRIPTION =
  "Живой монитор доступности Vercel, Railway, Render, Netlify, Heroku и Fly.io, измеренный с сервера в России: HTTP статус, задержка, TLS. Не официальный статус сервисов - независимые измерения с точки подключения в РФ.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "vercel не работает",
    "vercel недоступен из россии",
    "railway недоступен из россии",
    "render не открывается",
    "netlify не работает в россии",
    "heroku недоступен",
    "fly.io не работает",
    "проверка доступности vercel",
  ],
  alternates: {
    canonical: "/status",
    languages: {
      "ru-RU": "/status",
      "en-US": "/en/status",
      "x-default": "/status",
    },
  },
  openGraph: {
    type: "article",
    url: `${SITE_URL}/status`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud - RU Vantage Status Radar" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function StatusRadarLayout({ children }: { children: React.ReactNode }) {
  return children;
}

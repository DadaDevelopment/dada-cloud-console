import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Аналог Vercel в России — деплой из GitHub, оплата рублями";
const DESCRIPTION =
  "Аналог Vercel и Railway, который работает в России: деплой приложения из GitHub с HTTPS-адресом, оплата рублёвой картой, без VPN, данные и серверы в РФ (152-ФЗ). Тот же флоу «из репозитория в прод» — Dada Cloud.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "аналог vercel",
    "аналог vercel россия",
    "чем заменить vercel",
    "vercel оплата россия",
    "деплой из github россия",
    "аналог railway",
  ],
  alternates: {
    canonical: "/analog-vercel",
    languages: {
      "ru-RU": "/analog-vercel",
      "en-US": "/en/analog-vercel",
      "x-default": "/analog-vercel",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/analog-vercel`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — аналог Vercel в России" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function VercelAlternativeLayout({ children }: { children: React.ReactNode }) {
  return children;
}

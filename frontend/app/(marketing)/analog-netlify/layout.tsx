import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Аналог Netlify в России — деплой из GitHub, оплата рублями";
const DESCRIPTION =
  "Аналог Netlify, который работает в России: деплой сайта из GitHub-репозитория с HTTPS-адресом, открывается без VPN, оплата рублёвой картой, серверы в РФ (152-ФЗ) и managed PostgreSQL рядом. Тот же флоу «из репозитория в прод» — Dada Cloud.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "аналог netlify",
    "аналог netlify россия",
    "чем заменить netlify",
    "netlify не работает в россии",
    "замена netlify",
    "деплой сайта из github россия",
  ],
  alternates: {
    canonical: "/analog-netlify",
    languages: {
      "ru-RU": "/analog-netlify",
      "en-US": "/en/analog-netlify",
      "x-default": "/analog-netlify",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/analog-netlify`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — аналог Netlify в России" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function NetlifyAlternativeLayout({ children }: { children: React.ReactNode }) {
  return children;
}

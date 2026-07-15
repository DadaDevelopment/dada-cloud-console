import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Аналог Render в России — деплой из GitHub, оплата рублями";
const DESCRIPTION =
  "Аналог Render, который работает в России: деплой из GitHub-репозитория с HTTPS-адресом и managed PostgreSQL рядом, оплата рублёвой картой, без VPN, серверы в РФ (152-ФЗ). Тот же флоу «из репозитория в прод» — Dada Cloud.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "аналог render",
    "аналог render россия",
    "чем заменить render",
    "render оплата россия",
    "замена render",
    "деплой из github россия",
  ],
  alternates: {
    canonical: "/analog-render",
    languages: {
      "ru-RU": "/analog-render",
      "en-US": "/en/analog-render",
      "x-default": "/analog-render",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/analog-render`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — аналог Render в России" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function RenderAlternativeLayout({ children }: { children: React.ReactNode }) {
  return children;
}

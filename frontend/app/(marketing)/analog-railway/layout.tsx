import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Railway не принимает карту РФ? Аналог с оплатой в рублях";
const DESCRIPTION =
  "Российские карты Railway не принимает. Dada Cloud — тот же push-to-deploy из GitHub с managed PostgreSQL рядом, оплата рублями, серверы в РФ. Перенос — внутри.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "аналог railway",
    "аналог railway россия",
    "чем заменить railway",
    "railway оплата россия",
    "замена railway",
    "деплой из github россия",
  ],
  alternates: {
    canonical: "/analog-railway",
    languages: {
      "ru-RU": "/analog-railway",
      "en-US": "/en/analog-railway",
      "x-default": "/analog-railway",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/analog-railway`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — аналог Railway в России" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function RailwayAlternativeLayout({ children }: { children: React.ReactNode }) {
  return children;
}

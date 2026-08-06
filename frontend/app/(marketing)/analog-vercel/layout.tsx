import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Vercel не принимает карту РФ? Аналог с оплатой в рублях";
const DESCRIPTION =
  "Российские карты и «Мир» Vercel не принимает. Dada Cloud — тот же деплой из GitHub, но оплата рублями и серверы в РФ. Способы оплаты и перенос — внутри.";

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

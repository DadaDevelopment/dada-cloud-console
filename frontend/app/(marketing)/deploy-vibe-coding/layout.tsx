import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Хостинг для Lovable, Bolt и v0 в России — Dada Cloud";
const DESCRIPTION =
  "Навайбкодили приложение в Lovable, Bolt или v0? Задеплойте его в России в 1 клик: живой адрес с HTTPS за пару минут, бесплатный тариф, оплата рублём без VPN и зарубежных карт.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "хостинг lovable россия",
    "задеплоить приложение из россии",
    "деплой bolt.new",
    "деплой v0",
    "хостинг для vibe coding",
    "деплой replit проекта",
  ],
  alternates: {
    canonical: "/deploy-vibe-coding",
    languages: {
      "ru-RU": "/deploy-vibe-coding",
      "en-US": "/en/deploy-vibe-coding",
      "x-default": "/deploy-vibe-coding",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/deploy-vibe-coding`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — хостинг для Lovable, Bolt и v0 в России" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function VibeCodingDeployLayout({ children }: { children: React.ReactNode }) {
  return children;
}

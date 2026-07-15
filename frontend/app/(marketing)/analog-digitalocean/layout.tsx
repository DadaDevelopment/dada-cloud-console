import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Аналог DigitalOcean в России — серверы, деплой из GitHub, оплата рублями";
const DESCRIPTION =
  "Аналог DigitalOcean, который работает в России: свои серверы (App Servers) как Droplet, деплой приложений из GitHub как App Platform, managed PostgreSQL, оплата рублёвой картой и документы для юрлиц, серверы в РФ (152-ФЗ). Dada Cloud.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "аналог digitalocean",
    "аналог digitalocean россия",
    "чем заменить digitalocean",
    "digitalocean оплата россия",
    "замена digitalocean",
    "vps аналог droplet россия",
  ],
  alternates: {
    canonical: "/analog-digitalocean",
    languages: {
      "ru-RU": "/analog-digitalocean",
      "en-US": "/en/analog-digitalocean",
      "x-default": "/analog-digitalocean",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/analog-digitalocean`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — аналог DigitalOcean в России" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function DigitalOceanAlternativeLayout({ children }: { children: React.ReactNode }) {
  return children;
}

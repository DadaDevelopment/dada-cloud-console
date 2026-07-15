import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "DigitalOcean alternative for Russia — servers, GitHub deploys, pay in rubles";
const DESCRIPTION =
  "A DigitalOcean alternative that works in Russia: your own servers (App Servers) like Droplets, app deploys from GitHub like App Platform, managed PostgreSQL, pay with a Russian card with documents for legal entities, servers inside Russia (152-FZ). Dada Cloud.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "digitalocean alternative russia",
    "digitalocean alternative",
    "replace digitalocean",
    "digitalocean payment russia",
    "droplet alternative russia",
  ],
  alternates: {
    canonical: "/en/analog-digitalocean",
    languages: {
      "ru-RU": "/analog-digitalocean",
      "en-US": "/en/analog-digitalocean",
      "x-default": "/analog-digitalocean",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/analog-digitalocean`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — DigitalOcean alternative in Russia" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function EnDigitalOceanAlternativeLayout({ children }: { children: React.ReactNode }) {
  return children;
}

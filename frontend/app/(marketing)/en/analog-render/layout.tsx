import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Render alternative for Russia — deploy from GitHub, pay in rubles";
const DESCRIPTION =
  "A Render alternative that works in Russia: deploy from a GitHub repo with an HTTPS URL and managed PostgreSQL alongside, pay with a Russian card, no VPN, servers inside Russia (152-FZ). The same repo-to-production flow — Dada Cloud.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "render alternative russia",
    "render alternative",
    "replace render",
    "render payment russia",
    "deploy from github russia",
  ],
  alternates: {
    canonical: "/en/analog-render",
    languages: {
      "ru-RU": "/analog-render",
      "en-US": "/en/analog-render",
      "x-default": "/analog-render",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/analog-render`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — Render alternative in Russia" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function EnRenderAlternativeLayout({ children }: { children: React.ReactNode }) {
  return children;
}

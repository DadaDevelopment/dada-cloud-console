import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Netlify alternative for Russia — deploy from GitHub, pay in rubles";
const DESCRIPTION =
  "A Netlify alternative that works in Russia: deploy a site from a GitHub repo with an HTTPS URL, opens without a VPN, pay with a Russian card, servers inside Russia (152-FZ) and managed PostgreSQL alongside. The same repo-to-production flow — Dada Cloud.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "netlify alternative russia",
    "netlify alternative",
    "replace netlify",
    "netlify blocked russia",
    "deploy site from github russia",
  ],
  alternates: {
    canonical: "/en/analog-netlify",
    languages: {
      "ru-RU": "/analog-netlify",
      "en-US": "/en/analog-netlify",
      "x-default": "/analog-netlify",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/analog-netlify`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — Netlify alternative in Russia" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function EnNetlifyAlternativeLayout({ children }: { children: React.ReactNode }) {
  return children;
}

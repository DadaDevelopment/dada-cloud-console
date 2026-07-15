import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Railway alternative that works in Russia — deploy from GitHub";
const DESCRIPTION =
  "A Railway alternative that works in Russia: deploy from a GitHub repo with an HTTPS URL and managed PostgreSQL alongside, pay with a Russian card, no VPN, servers inside Russia (152-FZ). The same repo-to-production experience — Dada Cloud.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "railway alternative russia",
    "railway alternative",
    "replace railway",
    "railway payment russia",
    "deploy from github russia",
  ],
  alternates: {
    canonical: "/en/analog-railway",
    languages: {
      "ru-RU": "/analog-railway",
      "en-US": "/en/analog-railway",
      "x-default": "/analog-railway",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/analog-railway`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — Railway alternative in Russia" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function EnRailwayAlternativeLayout({ children }: { children: React.ReactNode }) {
  return children;
}

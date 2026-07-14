import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Vercel alternative that works in Russia — deploy from GitHub";
const DESCRIPTION =
  "A Vercel and Railway alternative that works in Russia: deploy an app from GitHub with an HTTPS URL, pay with a Russian card, no VPN, data and servers inside Russia (152-FZ). The same repo-to-production flow — Dada Cloud.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "vercel alternative russia",
    "vercel alternative",
    "railway alternative russia",
    "deploy from github russia",
  ],
  alternates: {
    canonical: "/en/analog-vercel",
    languages: {
      "ru-RU": "/analog-vercel",
      "en-US": "/en/analog-vercel",
      "x-default": "/analog-vercel",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/analog-vercel`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — Vercel alternative in Russia" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function EnVercelAlternativeLayout({ children }: { children: React.ReactNode }) {
  return children;
}

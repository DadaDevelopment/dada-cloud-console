import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Fly.io alternative for Russia — pay in rubles, no forwarders";
const DESCRIPTION =
  "Fly.io can't be paid directly with a Russian card — only through payment forwarders that add a markup. Dada Cloud gives you the same git-based deploy, but with direct ruble card payment and servers inside Russia, no forwarders, no VPN.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "fly.io alternative",
    "fly.io russia",
    "fly.io payment from russia",
    "replace fly.io",
    "fly.io russian card",
  ],
  alternates: {
    canonical: "/en/analog-fly-io",
    languages: {
      "ru-RU": "/analog-fly-io",
      "en-US": "/en/analog-fly-io",
      "x-default": "/analog-fly-io",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/analog-fly-io`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — Fly.io alternative for Russia" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function EnFlyIoAlternativeLayout({ children }: { children: React.ReactNode }) {
  return children;
}

import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Heroku alternative for Russia — deploy from GitHub, pay in rubles";
const DESCRIPTION =
  "A Heroku alternative that works in Russia: git push from a GitHub repo to production with an HTTPS URL and managed PostgreSQL alongside, pay with a Russian card, no VPN, servers inside Russia (152-FZ). The same flow as Heroku — Dada Cloud.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "heroku alternative russia",
    "heroku alternative",
    "replace heroku",
    "heroku payment russia",
    "deploy from github russia",
  ],
  alternates: {
    canonical: "/en/analog-heroku",
    languages: {
      "ru-RU": "/analog-heroku",
      "en-US": "/en/analog-heroku",
      "x-default": "/analog-heroku",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/analog-heroku`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — Heroku alternative in Russia" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function EnHerokuAlternativeLayout({ children }: { children: React.ReactNode }) {
  return children;
}

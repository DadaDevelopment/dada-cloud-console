import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Is Vercel down in Russia? Live availability check for Vercel, Railway, Render from RU";
const DESCRIPTION =
  "A live availability monitor for Vercel, Railway, Render, Netlify, Heroku and Fly.io, measured from a server in Russia: HTTP status, latency, TLS. Not an official status page - independent measurements from one RU vantage point.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "vercel down in russia",
    "railway unreachable from russia",
    "render not loading russia",
    "netlify down russia",
    "heroku unreachable russia",
    "fly.io down russia",
    "vercel availability check",
  ],
  alternates: {
    canonical: "/en/status",
    languages: {
      "ru-RU": "/status",
      "en-US": "/en/status",
      "x-default": "/status",
    },
  },
  openGraph: {
    type: "article",
    url: `${SITE_URL}/en/status`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud - RU Vantage Status Radar" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function EnStatusRadarLayout({ children }: { children: React.ReactNode }) {
  return children;
}

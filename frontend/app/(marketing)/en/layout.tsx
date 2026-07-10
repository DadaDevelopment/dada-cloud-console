import type { Metadata } from "next";

// Metadata for the English marketing subtree. The shared (marketing)/layout
// already provides the LangProvider + header/footer shell; this layout only
// overrides the locale-specific metadata and passes children straight through.
const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE_EN = "DADA Cloud — backend cloud: ship your backend from GitHub in minutes";
const DESCRIPTION_EN =
  "Backend cloud for founders, startups and agencies: connect a GitHub repo and get a working backend in minutes — Postgres, a domain, HTTPS and one-click rollback. No DevOps team, no infrastructure to manage.";

export const metadata: Metadata = {
  title: {
    default: TITLE_EN,
    template: "%s — DADA Cloud",
  },
  description: DESCRIPTION_EN,
  alternates: {
    canonical: "/en",
    languages: {
      "ru-RU": "/",
      "en-US": "/en",
      "x-default": "/",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en`,
    siteName: "DADA Cloud",
    title: TITLE_EN,
    description: DESCRIPTION_EN,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [
      {
        url: "/og.png",
        width: 1200,
        height: 630,
        alt: "DADA Cloud — ship your backend from GitHub in minutes",
      },
    ],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE_EN,
    description: DESCRIPTION_EN,
    images: ["/og.png"],
  },
};

export default function EnMarketingLayout({ children }: { children: React.ReactNode }) {
  return children;
}

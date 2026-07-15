import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Moving from Vercel to Dada Cloud - a step-by-step guide";
const DESCRIPTION =
  "How to move an existing project from Vercel to Dada Cloud: connect the repo, move environment variables, a custom domain with automatic TLS, and an honest vercel.json mapping table - what carries over 1:1, and what doesn't.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "migrate from vercel",
    "vercel migration guide",
    "move project from vercel",
    "vercel russia card",
    "vercel.json alternative",
    "deploy next.js in russia",
  ],
  alternates: {
    canonical: "/en/migrate-vercel",
    languages: {
      "ru-RU": "/migrate-vercel",
      "en-US": "/en/migrate-vercel",
      "x-default": "/migrate-vercel",
    },
  },
  openGraph: {
    type: "article",
    url: `${SITE_URL}/en/migrate-vercel`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud - moving from Vercel" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function EnMigrateVercelLayout({ children }: { children: React.ReactNode }) {
  return children;
}

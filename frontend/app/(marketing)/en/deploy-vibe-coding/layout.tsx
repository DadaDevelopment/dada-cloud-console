import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "How to deploy a v0, Lovable, Bolt or Cursor project — Dada Cloud";
const DESCRIPTION =
  "Built an app in v0, Lovable, Bolt or Cursor? Export the code to GitHub and deploy it with the same git push to Dada Cloud: a live HTTPS URL in minutes, pay with a Russian card, servers in Russia, no VPN or foreign card needed.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "deploy v0 project",
    "deploy lovable app",
    "deploy bolt.new project",
    "hosting for vibe coding apps",
    "deploy ai generated app russia",
  ],
  alternates: {
    canonical: "/en/deploy-vibe-coding",
    languages: {
      "ru-RU": "/deploy-vibe-coding",
      "en-US": "/en/deploy-vibe-coding",
      "x-default": "/deploy-vibe-coding",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/deploy-vibe-coding`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — deploy v0, Lovable, Bolt, Cursor projects" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function EnVibeCodingDeployLayout({ children }: { children: React.ReactNode }) {
  return children;
}

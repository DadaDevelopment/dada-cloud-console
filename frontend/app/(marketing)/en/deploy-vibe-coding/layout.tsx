import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Deploy your Lovable, Bolt or v0 app from Russia — Dada Cloud";
const DESCRIPTION =
  "Vibe-coded an app with Lovable, Bolt or v0? Deploy it in one click from Russia: a live HTTPS URL in minutes, free tier, no card required, pay in rubles when you need more.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "lovable hosting russia",
    "deploy app from russia",
    "deploy bolt.new project",
    "deploy v0 project",
    "hosting for vibe coding apps",
    "deploy replit project russia",
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
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — hosting for Lovable, Bolt and v0 apps" }],
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

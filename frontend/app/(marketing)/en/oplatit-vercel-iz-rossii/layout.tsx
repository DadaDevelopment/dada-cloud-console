import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "How to pay for Vercel from Russia in 2026 — routes, risks, alternatives";
const DESCRIPTION =
  "Paying for Vercel from Russia: why Russian and Mir cards are declined, what really happens with CIS and virtual cards, what it costs a production project, and how to move the app to a platform that bills in roubles.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "pay for vercel from russia",
    "vercel card declined russia",
    "vercel payment russia",
    "vercel alternative russia",
  ],
  alternates: {
    canonical: "/en/oplatit-vercel-iz-rossii",
    languages: {
      "ru-RU": "/oplatit-vercel-iz-rossii",
      "en-US": "/en/oplatit-vercel-iz-rossii",
      "x-default": "/oplatit-vercel-iz-rossii",
    },
  },
  openGraph: {
    type: "article",
    url: `${SITE_URL}/en/oplatit-vercel-iz-rossii`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "Paying for Vercel from Russia" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function PayVercelEnLayout({ children }: { children: React.ReactNode }) {
  return children;
}

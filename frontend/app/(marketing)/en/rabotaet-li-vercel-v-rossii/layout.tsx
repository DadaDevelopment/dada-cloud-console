import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Does Vercel work in Russia in 2026 — what opens and what broke";
const DESCRIPTION =
  "Part by part: sites on Vercel do open from Russia, signup is not guaranteed, payments fail, and storing personal data there does not meet 152-FZ. How to check your own project in five minutes and what to do next.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "does vercel work in russia",
    "is vercel blocked in russia",
    "vercel russia 2026",
    "vercel alternative russia",
  ],
  alternates: {
    canonical: "/en/rabotaet-li-vercel-v-rossii",
    languages: {
      "ru-RU": "/rabotaet-li-vercel-v-rossii",
      "en-US": "/en/rabotaet-li-vercel-v-rossii",
      "x-default": "/rabotaet-li-vercel-v-rossii",
    },
  },
  openGraph: {
    type: "article",
    url: `${SITE_URL}/en/rabotaet-li-vercel-v-rossii`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "Does Vercel work in Russia" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function VercelInRussiaEnLayout({ children }: { children: React.ReactNode }) {
  return children;
}

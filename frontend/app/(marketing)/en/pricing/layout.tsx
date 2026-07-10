import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "DADA Cloud pricing — Free, Startup, Business, Enterprise";
const DESCRIPTION =
  "Transparent plans: pay for a plan, not per resource. Free tier, Startup from $12/mo, Business from $35/mo and Enterprise with SLA. A recommender picks the right plan for your needs.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  alternates: {
    canonical: "/en/pricing",
    languages: {
      "ru-RU": "/pricing",
      "en-US": "/en/pricing",
      "x-default": "/pricing",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/pricing`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — pricing and plans" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function EnPricingLayout({ children }: { children: React.ReactNode }) {
  return children;
}

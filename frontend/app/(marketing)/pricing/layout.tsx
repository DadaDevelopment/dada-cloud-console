import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Цены DADA Cloud — Free, Startup, Business, Enterprise";
const DESCRIPTION =
  "Прозрачные планы: платите за план, а не за каждый ресурс. Бесплатный Free, Startup от 990 ₽/мес, Business от 2 900 ₽/мес и Enterprise с SLA. Подбор плана под ваши потребности.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  alternates: {
    canonical: "/pricing",
    languages: {
      "ru-RU": "/pricing",
      "en-US": "/en/pricing",
      "x-default": "/pricing",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/pricing`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — тарифы и цены" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function PricingLayout({ children }: { children: React.ReactNode }) {
  return children;
}

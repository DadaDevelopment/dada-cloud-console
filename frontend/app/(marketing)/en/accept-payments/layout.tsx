import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Accept payments in your app via YooKassa - Dada Cloud";
const DESCRIPTION =
  "Connect your own YooKassa store to your app via OAuth, right from the Dada Cloud console. The platform never sees your store's secret key, money goes straight to your store's account, and webhooks arrive in your app. Requires your own YooKassa store (sole proprietor, LLC, or self-employed).";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "приём платежей ЮKassa",
    "подключить ЮKassa к приложению",
    "оплата в приложении",
    "ЮKassa OAuth",
    "интеграция ЮKassa",
  ],
  alternates: {
    canonical: "/en/accept-payments",
    languages: {
      "ru-RU": "/accept-payments",
      "en-US": "/en/accept-payments",
      "x-default": "/accept-payments",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/accept-payments`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function Layout({ children }: { children: React.ReactNode }) {
  return children;
}

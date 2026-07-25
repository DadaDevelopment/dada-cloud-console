import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Приём платежей в приложении через ЮKassa - Dada Cloud";
const DESCRIPTION =
  "Подключите свой магазин ЮKassa к приложению через OAuth прямо из консоли Dada Cloud. Секретный ключ магазина платформе не передаётся, деньги идут напрямую на счёт вашего магазина, вебхуки приходят в приложение. Нужен собственный магазин ЮKassa (ИП, ООО или самозанятый).";

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
    canonical: "/accept-payments",
    languages: {
      "ru-RU": "/accept-payments",
      "en-US": "/en/accept-payments",
      "x-default": "/accept-payments",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/accept-payments`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
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

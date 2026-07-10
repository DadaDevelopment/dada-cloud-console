import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Управляемый PostgreSQL с автоматическим DATABASE_URL";
const DESCRIPTION =
  "Управляемый Postgres рядом с приложением: DATABASE_URL прокидывается автоматически, бэкапы по расписанию, мониторинг и обновления — на нас. Без сервера для патчинга и ручной сборки строки подключения.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  alternates: {
    canonical: "/databases",
    languages: {
      "ru-RU": "/databases",
      "en-US": "/en/databases",
      "x-default": "/databases",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/databases`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — управляемый PostgreSQL" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function DatabasesLayout({ children }: { children: React.ReactNode }) {
  return children;
}

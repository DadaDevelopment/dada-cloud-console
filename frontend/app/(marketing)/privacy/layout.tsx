import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Политика обработки персональных данных";
const DESCRIPTION =
  "Как DADA Cloud обрабатывает персональные данные: какие данные собираются, где хранятся (серверы в России, 152-ФЗ) и какие права есть у пользователя.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  alternates: {
    canonical: "/privacy",
    languages: {
      "ru-RU": "/privacy",
      "en-US": "/en/privacy",
      "x-default": "/privacy",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/privacy`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
  },
  robots: { index: true, follow: true },
};

export default function PrivacyLayout({ children }: { children: React.ReactNode }) {
  return children;
}

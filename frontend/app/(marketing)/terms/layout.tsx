import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Пользовательское соглашение";
const DESCRIPTION =
  "Условия использования облачной платформы DADA Cloud: аккаунт, тарифы, допустимое использование, ответственность и контакты.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  alternates: {
    canonical: "/terms",
    languages: {
      "ru-RU": "/terms",
      "en-US": "/en/terms",
      "x-default": "/terms",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/terms`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
  },
  robots: { index: true, follow: true },
};

export default function TermsLayout({ children }: { children: React.ReactNode }) {
  return children;
}

import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Хостинг Streamlit в России - Dada Cloud";
const DESCRIPTION =
  "Задеплойте Streamlit-дашборд на серверах в России: Dockerfile из пяти строк, HTTPS-домен сразу, ссылка открывается у всех без VPN. Бесплатный тариф.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "хостинг streamlit",
    "куда задеплоить streamlit",
    "streamlit cloud альтернатива",
    "хостинг streamlit россия",
    "деплой streamlit",
  ],
  alternates: {
    canonical: "/hosting-streamlit",
    languages: {
      "ru-RU": "/hosting-streamlit",
      "en-US": "/en/hosting-streamlit",
      "x-default": "/hosting-streamlit",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/hosting-streamlit`,
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

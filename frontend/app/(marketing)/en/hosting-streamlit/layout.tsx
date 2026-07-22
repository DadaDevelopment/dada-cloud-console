import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Streamlit hosting in Russia - Dada Cloud";
const DESCRIPTION =
  "Deploy a Streamlit dashboard on servers in Russia: a five-line Dockerfile, an HTTPS domain right away, the link opens for everyone without a VPN. Free tier.";

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
    canonical: "/en/hosting-streamlit",
    languages: {
      "ru-RU": "/hosting-streamlit",
      "en-US": "/en/hosting-streamlit",
      "x-default": "/hosting-streamlit",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/hosting-streamlit`,
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

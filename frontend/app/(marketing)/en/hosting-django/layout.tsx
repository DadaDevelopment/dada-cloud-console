import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Django hosting without your own server - Dada Cloud";
const DESCRIPTION =
  "Deploy a Django project without server setup: the platform detects django, builds the project and runs it with PostgreSQL and an HTTPS domain. Free tier, servers in Russia.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "хостинг django",
    "куда задеплоить django",
    "бесплатный хостинг django",
    "хостинг django проекта",
    "деплой django",
  ],
  alternates: {
    canonical: "/en/hosting-django",
    languages: {
      "ru-RU": "/hosting-django",
      "en-US": "/en/hosting-django",
      "x-default": "/hosting-django",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/hosting-django`,
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

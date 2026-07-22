import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "FastAPI hosting without Docker - Dada Cloud";
const DESCRIPTION =
  "Deploy FastAPI in minutes: connect a repo, the platform detects the framework, builds and runs the app with an HTTPS domain. Crashes restart themselves. Free tier, servers in Russia.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "хостинг fastapi",
    "куда задеплоить fastapi",
    "бесплатный хостинг fastapi",
    "хостинг python api",
    "деплой fastapi без docker",
  ],
  alternates: {
    canonical: "/en/hosting-fastapi",
    languages: {
      "ru-RU": "/hosting-fastapi",
      "en-US": "/en/hosting-fastapi",
      "x-default": "/hosting-fastapi",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/hosting-fastapi`,
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

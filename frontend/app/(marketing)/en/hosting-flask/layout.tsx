import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Flask hosting without a server - Dada Cloud";
const DESCRIPTION =
  "Deploy a Flask app without your own server: connect a repo, the platform builds and runs it with an HTTPS domain. Crashes restart themselves. Free tier, servers in Russia.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "хостинг flask",
    "куда задеплоить flask",
    "бесплатный хостинг flask",
    "хостинг flask приложения",
    "деплой flask без docker",
  ],
  alternates: {
    canonical: "/en/hosting-flask",
    languages: {
      "ru-RU": "/hosting-flask",
      "en-US": "/en/hosting-flask",
      "x-default": "/hosting-flask",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/hosting-flask`,
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

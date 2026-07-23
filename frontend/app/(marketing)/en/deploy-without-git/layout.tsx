import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Deploy without git: upload a zip, get a live URL - Dada Cloud";
const DESCRIPTION =
  "Zip your project as zip or tar.gz (up to 100MB) and drop it into the Dada Cloud console. The platform detects your framework, builds the app and issues an HTTPS address in 1-2 minutes. No git or GitHub required. Works with exports from Lovable, Bolt and v0.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "деплой без git",
    "загрузить zip и задеплоить",
    "хостинг без github",
    "деплой архива",
    "загрузить проект без репозитория",
  ],
  alternates: {
    canonical: "/en/deploy-without-git",
    languages: {
      "ru-RU": "/deploy-without-git",
      "en-US": "/en/deploy-without-git",
      "x-default": "/deploy-without-git",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/deploy-without-git`,
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

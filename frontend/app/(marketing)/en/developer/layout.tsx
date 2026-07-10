import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Developer API and how-to guides";
const DESCRIPTION =
  "Token-authenticated /api/v1 REST API and step-by-step guides: deploy from GitHub, App Servers (BYO-VM), managed Postgres, domains and HTTPS, monitoring. Everything the console does is available over the API.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  alternates: {
    canonical: "/en/developer",
    languages: {
      "ru-RU": "/developer",
      "en-US": "/en/developer",
      "x-default": "/developer",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/developer`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — developer API and guides" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function EnDeveloperLayout({ children }: { children: React.ReactNode }) {
  return children;
}

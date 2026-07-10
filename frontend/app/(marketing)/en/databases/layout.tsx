import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Managed PostgreSQL with automatic DATABASE_URL";
const DESCRIPTION =
  "Managed Postgres next to your app: DATABASE_URL is injected automatically, scheduled backups, monitoring and upgrades handled for you. No server to patch, no connection string to assemble by hand.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  alternates: {
    canonical: "/en/databases",
    languages: {
      "ru-RU": "/databases",
      "en-US": "/en/databases",
      "x-default": "/databases",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/databases`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — managed PostgreSQL" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function EnDatabasesLayout({ children }: { children: React.ReactNode }) {
  return children;
}

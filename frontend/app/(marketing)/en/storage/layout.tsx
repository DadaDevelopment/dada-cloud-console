import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "S3-compatible object storage (Beta)";
const DESCRIPTION =
  "S3-compatible object storage for backups, media and static assets. Works with aws-cli, s3cmd and S3 SDKs, pay for what you store. Currently in Beta.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  alternates: {
    canonical: "/en/storage",
    languages: {
      "ru-RU": "/storage",
      "en-US": "/en/storage",
      "x-default": "/storage",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/storage`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — S3-compatible object storage" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function EnStorageLayout({ children }: { children: React.ReactNode }) {
  return children;
}

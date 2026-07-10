import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "S3-совместимое объектное хранилище (Beta)";
const DESCRIPTION =
  "S3-совместимое объектное хранилище для бэкапов, медиа и статики. Работает с aws-cli, s3cmd и S3-SDK, оплата по объёму. Сейчас в статусе Beta.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  alternates: {
    canonical: "/storage",
    languages: {
      "ru-RU": "/storage",
      "en-US": "/en/storage",
      "x-default": "/storage",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/storage`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — S3-совместимое объектное хранилище" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function StorageLayout({ children }: { children: React.ReactNode }) {
  return children;
}

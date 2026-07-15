import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Аналог Fly.io для России — оплата рублями без посредников";
const DESCRIPTION =
  "Fly.io нельзя оплатить российской картой напрямую — только через платёжных посредников с наценкой. Dada Cloud даёт тот же git-деплой, но с прямой оплатой рублёвой картой и серверами в России, без посредников и VPN.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "аналог fly.io",
    "fly.io россия",
    "fly.io оплата из россии",
    "чем заменить fly.io",
    "fly.io российская карта",
  ],
  alternates: {
    canonical: "/analog-fly-io",
    languages: {
      "ru-RU": "/analog-fly-io",
      "en-US": "/en/analog-fly-io",
      "x-default": "/analog-fly-io",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/analog-fly-io`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — аналог Fly.io для России" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function FlyIoAlternativeLayout({ children }: { children: React.ReactNode }) {
  return children;
}

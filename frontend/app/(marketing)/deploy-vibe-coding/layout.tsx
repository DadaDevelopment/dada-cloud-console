import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Как задеплоить проект из v0, Lovable, Bolt, Cursor — Dada Cloud";
const DESCRIPTION =
  "Собрали приложение в v0, Lovable, Bolt или Cursor? Экспортируйте код в GitHub и разверните его тем же git push в Dada Cloud: живой HTTPS-адрес за минуты, оплата рублёвой картой, серверы в России, без VPN и зарубежных карт.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "деплой v0",
    "деплой lovable",
    "деплой bolt.new",
    "хостинг для vibe coding",
    "как задеплоить приложение из cursor",
    "деплой ai-приложения в россии",
  ],
  alternates: {
    canonical: "/deploy-vibe-coding",
    languages: {
      "ru-RU": "/deploy-vibe-coding",
      "en-US": "/en/deploy-vibe-coding",
      "x-default": "/deploy-vibe-coding",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/deploy-vibe-coding`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — деплой проектов из v0, Lovable, Bolt, Cursor" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function VibeCodingDeployLayout({ children }: { children: React.ReactNode }) {
  return children;
}

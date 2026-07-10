import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Свой VPS под управлением или новая VM — App Servers";
const DESCRIPTION =
  "Подключите свой сервер по SSH или закажите новую VM: деплой, домены, базы и мониторинг всех серверов в одной панели. Заберите уже работающие Docker-контейнеры без пересборки — альтернатива Coolify для команд и агентств.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  alternates: {
    canonical: "/cloud-servers",
    languages: {
      "ru-RU": "/cloud-servers",
      "en-US": "/en/cloud-servers",
      "x-default": "/cloud-servers",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/cloud-servers`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud App Servers — свой VPS под управлением" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function CloudServersLayout({ children }: { children: React.ReactNode }) {
  return children;
}

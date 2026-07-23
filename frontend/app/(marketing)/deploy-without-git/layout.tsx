import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Деплой без git: залей zip - получи рабочий адрес - Dada Cloud";
const DESCRIPTION =
  "Заархивируйте проект в zip или tar.gz (до 100MB) и перетащите в консоль Dada Cloud. Платформа сама определит фреймворк, соберёт приложение и выдаст HTTPS-адрес за 1-2 минуты. Git и GitHub не нужны. Подходит для экспорта из Lovable, Bolt и v0.";

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
    canonical: "/deploy-without-git",
    languages: {
      "ru-RU": "/deploy-without-git",
      "en-US": "/en/deploy-without-git",
      "x-default": "/deploy-without-git",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/deploy-without-git`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
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

import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Работает ли Vercel в России в 2026 — что открывается, что отвалилось";
const DESCRIPTION =
  "Разбор по частям: сайты на Vercel из России открываются, регистрация не гарантирована, оплата не проходит, а хранение персональных данных не соответствует 152-ФЗ. Как за пять минут проверить свой проект и что делать дальше.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "работает ли vercel в россии",
    "vercel заблокирован в россии",
    "работают ли сайты на vercel в россии",
    "vercel россия 2026",
    "vercel медленно открывается",
    "аналог vercel в россии",
  ],
  alternates: {
    canonical: "/rabotaet-li-vercel-v-rossii",
    languages: {
      "ru-RU": "/rabotaet-li-vercel-v-rossii",
      "en-US": "/en/rabotaet-li-vercel-v-rossii",
      "x-default": "/rabotaet-li-vercel-v-rossii",
    },
  },
  openGraph: {
    type: "article",
    url: `${SITE_URL}/rabotaet-li-vercel-v-rossii`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "Работает ли Vercel в России" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function VercelInRussiaLayout({ children }: { children: React.ReactNode }) {
  return children;
}

import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Как оплатить Vercel из России в 2026 — способы, риски и чем заменить";
const DESCRIPTION =
  "Оплата Vercel из России: почему карты российских банков и «Мир» не проходят, что бывает с картами СНГ и виртуальными картами, чем это грозит боевому проекту и как перенести приложение на площадку с рублёвой оплатой за один вечер.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "оплата vercel из россии",
    "оплатить vercel",
    "оплата vercel для россиян",
    "как оплатить vercel картой мир",
    "vercel не принимает карту",
    "чем заменить vercel россия",
  ],
  alternates: {
    canonical: "/oplatit-vercel-iz-rossii",
    languages: {
      "ru-RU": "/oplatit-vercel-iz-rossii",
      "en-US": "/en/oplatit-vercel-iz-rossii",
      "x-default": "/oplatit-vercel-iz-rossii",
    },
  },
  openGraph: {
    type: "article",
    url: `${SITE_URL}/oplatit-vercel-iz-rossii`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "Оплата Vercel из России — разбор способов" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function PayVercelLayout({ children }: { children: React.ReactNode }) {
  return children;
}

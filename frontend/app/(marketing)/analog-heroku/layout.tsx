import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Heroku не принимает карту РФ? Аналог с оплатой в рублях";
const DESCRIPTION =
  "Российские карты Heroku не принимает. Dada Cloud — тот же git push в прод с managed PostgreSQL рядом, оплата рублями, серверы в РФ. Как перенести — внутри.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "аналог heroku",
    "аналог heroku россия",
    "чем заменить heroku",
    "heroku оплата россия",
    "замена heroku",
    "деплой из github россия",
  ],
  alternates: {
    canonical: "/analog-heroku",
    languages: {
      "ru-RU": "/analog-heroku",
      "en-US": "/en/analog-heroku",
      "x-default": "/analog-heroku",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/analog-heroku`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud — аналог Heroku для России" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function HerokuAlternativeLayout({ children }: { children: React.ReactNode }) {
  return children;
}

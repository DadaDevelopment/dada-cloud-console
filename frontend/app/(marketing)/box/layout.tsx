import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Dada Box — тело для твоего агента";
const DESCRIPTION =
  "Бокс с рутом поднимается сам за секунды из тёплого пула: твой Claude, Cursor или Codex подключается и работает как на своей машине. Публичный адрес с TLS, тарификация по активным минутам. Кристаллизация в постоянную VM — следующий шаг.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "окружение для claude code",
    "запустить агента в облаке",
    "песочница для ai агента",
    "параллельные агенты",
    "удалённая среда для разработки россия",
    "cursor codex сервер",
  ],
  alternates: {
    canonical: "/box",
    languages: {
      "ru-RU": "/box",
      "en-US": "/en/box",
      "x-default": "/box",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/box`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "Dada Box — тело для твоего агента" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function BoxLayout({ children }: { children: React.ReactNode }) {
  return children;
}

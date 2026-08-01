import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Dada Box — a body for your agent";
const DESCRIPTION =
  "A root box boots itself in seconds from a warm pool: your Claude, Cursor or Codex connects and works like it owns the machine. A public address with TLS, billed per active minute. Crystallizing into a permanent VM is next.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "cloud environment for claude code",
    "run coding agent in the cloud",
    "agent sandbox with root",
    "parallel coding agents",
    "remote dev environment for agents",
    "cursor codex remote box",
  ],
  alternates: {
    canonical: "/en/box",
    languages: {
      "ru-RU": "/box",
      "en-US": "/en/box",
      "x-default": "/box",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/box`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "Dada Box — a body for your agent" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function BoxLayoutEn({ children }: { children: React.ReactNode }) {
  return children;
}

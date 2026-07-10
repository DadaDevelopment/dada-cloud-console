import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Bring your own VPS or a managed VM — App Servers";
const DESCRIPTION =
  "Connect your own server over SSH or order a new VM: deploys, domains, databases and monitoring for every server in one panel. Adopt already-running Docker containers with no rebuild — the Coolify alternative for teams and agencies.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  alternates: {
    canonical: "/en/cloud-servers",
    languages: {
      "ru-RU": "/cloud-servers",
      "en-US": "/en/cloud-servers",
      "x-default": "/cloud-servers",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/cloud-servers`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
    images: [{ url: "/og.png", width: 1200, height: 630, alt: "DADA Cloud App Servers — bring your own VPS" }],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
};

export default function EnCloudServersLayout({ children }: { children: React.ReactNode }) {
  return children;
}

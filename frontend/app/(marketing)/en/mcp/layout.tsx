import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "DADA Cloud MCP server: run your cloud from Claude - Dada Cloud";
const DESCRIPTION =
  "Connect DADA Cloud to Claude Code, Claude Desktop or Cursor as an MCP server: 41 console tools - deploys, servers, databases, domains, logs - straight from chat. Browser sign-in with your DADA ID, no tokens in config files. URL: https://console.dada-tuda.ru/mcp.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "mcp server hosting",
    "dada cloud mcp",
    "deploy from claude",
    "model context protocol cloud",
    "claude code deploy app",
  ],
  alternates: {
    canonical: "/en/mcp",
    languages: {
      "ru-RU": "/mcp",
      "en-US": "/en/mcp",
      "x-default": "/mcp",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/en/mcp`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "en_US",
    alternateLocale: ["ru_RU"],
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

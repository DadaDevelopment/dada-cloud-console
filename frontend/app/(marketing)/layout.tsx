import type { Metadata } from "next";
import { LangProvider } from "@/lib/i18n/context";
import { MarketingHeader } from "@/components/marketing/header";
import { MarketingFooter } from "@/components/marketing/footer";
import { BreadcrumbJsonLd } from "@/components/marketing/breadcrumb-jsonld";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "DADA Cloud — бэкенд-облако: задеплой бэкенд из GitHub за минуты";
const DESCRIPTION =
  "Бэкенд-облако для основателей, стартапов и агентств: подключи GitHub-репозиторий и за минуты получи рабочий бэкенд с Postgres, доменом, HTTPS и откатом в один клик. Без DevOps и сложного Kubernetes.";

export const metadata: Metadata = {
  metadataBase: new URL(SITE_URL),
  title: {
    default: TITLE,
    template: "%s — DADA Cloud",
  },
  description: DESCRIPTION,
  applicationName: "DADA Cloud",
  alternates: {
    // No blanket canonical here: this layout wraps every marketing route, so a
    // hardcoded "/" would wrongly canonicalize /pricing etc. to the home page.
    // Each URL self-canonicalizes; the /en subtree sets its own canonical.
    languages: {
      "ru-RU": "/",
      "en-US": "/en",
      "x-default": "/",
    },
  },
  openGraph: {
    type: "website",
    url: SITE_URL,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
    images: [
      {
        url: "/og.png",
        width: 1200,
        height: 630,
        alt: "DADA Cloud — задеплой бэкенд из GitHub за минуты",
      },
    ],
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESCRIPTION,
    images: ["/og.png"],
  },
  robots: {
    index: true,
    follow: true,
    "max-image-preview": "large",
    "max-snippet": -1,
    "max-video-preview": -1,
  },
};

export default function MarketingLayout({ children }: { children: React.ReactNode }) {
  return (
    <LangProvider>
      <div className="flex min-h-screen flex-col bg-white">
        <BreadcrumbJsonLd />
        <MarketingHeader />
        <main className="flex-1">{children}</main>
        <MarketingFooter />
      </div>
    </LangProvider>
  );
}

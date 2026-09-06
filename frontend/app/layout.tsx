import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import { headers } from "next/headers";
import "./globals.css";
import { AuthProvider } from "@/lib/auth";
import { CookieConsent } from "@/components/cookie-consent";
import { UxTelemetryProvider } from "@/components/ux-telemetry-provider";
import { describePath, SITE_NAME } from "@/lib/page-title";

const geist = Geist({ subsets: ["latin"], variable: "--font-geist-sans" });
const geistMono = Geist_Mono({ subsets: ["latin"], variable: "--font-geist-mono" });

/**
 * Search engine ownership tokens. Read from server env rather than a
 * NEXT_PUBLIC_ variable so a verification can be claimed by setting a value on
 * the deployment, with no rebuild: the root layout calls {@link headers}, which
 * makes every route render dynamically, so the meta tag is resolved per request.
 *
 * Google is the one that matters. It has never crawled this site, and since the
 * public sitemap ping endpoint was retired, Search Console is the only way to
 * hand it a sitemap directly.
 */
const GOOGLE_VERIFICATION = process.env.GOOGLE_SITE_VERIFICATION;
const YANDEX_VERIFICATION = process.env.YANDEX_SITE_VERIFICATION;

/**
 * Metadata for every route that does not set its own — in practice the whole
 * console, whose pages are client components and therefore cannot export any.
 * The route is read from the `x-dada-path` header the proxy stamps, because
 * `generateMetadata` here only sees the root segment's (empty) params.
 *
 * This is what a pasted console link unfurls as in a chat, so it derives the
 * title from the path alone and never from a lookup: the crawler that fetches
 * the preview has no session. Marketing routes override all of it in their own
 * layouts.
 */
export async function generateMetadata(): Promise<Metadata> {
  const headerList = await headers();
  const path = headerList.get("x-dada-path") ?? "/";
  const locale = headerList.get("x-dada-locale") === "en" ? "en" : "ru";
  const { title, description } = describePath(path, locale);
  return {
    metadataBase: new URL("https://cloud.dada-tuda.ru"),
    title,
    description,
    openGraph: {
      type: "website",
      siteName: SITE_NAME,
      title,
      description,
      locale: locale === "en" ? "en_US" : "ru_RU",
    },
    verification: {
      ...(GOOGLE_VERIFICATION ? { google: GOOGLE_VERIFICATION } : {}),
      ...(YANDEX_VERIFICATION ? { yandex: YANDEX_VERIFICATION } : {}),
    },
  };
}

export default async function RootLayout({ children }: { children: React.ReactNode }) {
  // The proxy sets x-dada-locale on the marketing host so the right <html lang>
  // is emitted on SSR (RU at "/", EN at "/en"). Falls back to "en" elsewhere.
  const lang = (await headers()).get("x-dada-locale") ?? "en";
  return (
    <html lang={lang} className={`${geist.variable} ${geistMono.variable} h-full`}>
      <body className="h-full bg-gray-50 antialiased">
        <CookieConsent lang={lang} />
        <UxTelemetryProvider />
        <AuthProvider>{children}</AuthProvider>
      </body>
    </html>
  );
}

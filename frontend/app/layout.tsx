import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import { headers } from "next/headers";
import "./globals.css";
import { AuthProvider } from "@/lib/auth";
import { YandexMetrika } from "@/components/yandex-metrika";
import { UxTelemetryProvider } from "@/components/ux-telemetry-provider";

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

export const metadata: Metadata = {
  metadataBase: new URL("https://cloud.dada-tuda.ru"),
  title: "DADA Cloud Console",
  description: "GitOps-backed self-service cloud console",
  verification: {
    ...(GOOGLE_VERIFICATION ? { google: GOOGLE_VERIFICATION } : {}),
    ...(YANDEX_VERIFICATION ? { yandex: YANDEX_VERIFICATION } : {}),
  },
};

export default async function RootLayout({ children }: { children: React.ReactNode }) {
  // The proxy sets x-dada-locale on the marketing host so the right <html lang>
  // is emitted on SSR (RU at "/", EN at "/en"). Falls back to "en" elsewhere.
  const lang = (await headers()).get("x-dada-locale") ?? "en";
  return (
    <html lang={lang} className={`${geist.variable} ${geistMono.variable} h-full`}>
      <body className="h-full bg-gray-50 antialiased">
        <YandexMetrika />
        <UxTelemetryProvider />
        <AuthProvider>{children}</AuthProvider>
      </body>
    </html>
  );
}

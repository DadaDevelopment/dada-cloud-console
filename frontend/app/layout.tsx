import type { Metadata } from "next";
import { Geist } from "next/font/google";
import { headers } from "next/headers";
import "./globals.css";
import { AuthProvider } from "@/lib/auth";
import { YandexMetrika } from "@/components/yandex-metrika";

const geist = Geist({ subsets: ["latin"], variable: "--font-geist" });

export const metadata: Metadata = {
  metadataBase: new URL("https://cloud.dada-tuda.ru"),
  title: "DADA Cloud Console",
  description: "GitOps-backed self-service cloud console",
};

export default async function RootLayout({ children }: { children: React.ReactNode }) {
  // The proxy sets x-dada-locale on the marketing host so the right <html lang>
  // is emitted on SSR (RU at "/", EN at "/en"). Falls back to "en" elsewhere.
  const lang = (await headers()).get("x-dada-locale") ?? "en";
  return (
    <html lang={lang} className={`${geist.variable} h-full`}>
      <body className="h-full bg-gray-50 antialiased">
        <YandexMetrika />
        <AuthProvider>{children}</AuthProvider>
      </body>
    </html>
  );
}

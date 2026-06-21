import type { Metadata } from "next";
import { LangProvider } from "@/lib/i18n/context";
import { MarketingHeader } from "@/components/marketing/header";
import { MarketingFooter } from "@/components/marketing/footer";

export const metadata: Metadata = {
  title: "DADA Cloud — облачная платформа",
  description:
    "Облачная платформа на основе GitOps: виртуальные серверы, Kubernetes, управляемые базы данных, объектное хранилище и CDN в одной консоли.",
};

export default function MarketingLayout({ children }: { children: React.ReactNode }) {
  return (
    <LangProvider>
      <div className="flex min-h-screen flex-col bg-white">
        <MarketingHeader />
        <main className="flex-1">{children}</main>
        <MarketingFooter />
      </div>
    </LangProvider>
  );
}

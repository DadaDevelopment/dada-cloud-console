"use client";

import Link from "next/link";
import { useLang } from "@/lib/i18n/context";
import { localeHref } from "@/lib/site";
import { ProductHero, FeatureGrid, FaqList, PillList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

export default function DatabasesPage() {
  const { t, locale } = useLang();
  return (
    <>
      <FaqJsonLd path="/databases" items={t.databases.faq} />
      <ProductHero title={t.databases.heroTitle} subtitle={t.databases.heroSubtitle} />
      <section className="bg-white pt-16">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <PillList items={t.databases.engines} />
          <p className="mt-4 text-sm text-slate-500">
            {t.databases.note}{" "}
            <Link href={localeHref("/cloud-servers", locale)} className="font-medium text-blue-600 hover:text-blue-700">
              {t.databases.noteLinkLabel}
            </Link>
          </p>
        </div>
      </section>
      <FeatureGrid title={t.databases.featuresTitle} features={t.databases.features} />
      <FaqList title={t.databases.faqTitle} items={t.databases.faq} />
      <CtaBand />
    </>
  );
}

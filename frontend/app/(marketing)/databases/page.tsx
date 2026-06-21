"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, PillList, CtaBand } from "@/components/marketing/sections";

export default function DatabasesPage() {
  const { t } = useLang();
  return (
    <>
      <ProductHero title={t.databases.heroTitle} subtitle={t.databases.heroSubtitle} />
      <section className="bg-white pt-16">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <PillList items={t.databases.engines} />
        </div>
      </section>
      <FeatureGrid features={t.databases.features} />
      <CtaBand />
    </>
  );
}

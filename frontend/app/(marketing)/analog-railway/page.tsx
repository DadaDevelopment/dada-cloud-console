"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

export default function RailwayAlternativePage() {
  const { t } = useLang();
  return (
    <>
      <FaqJsonLd path="/analog-railway" items={t.railwayAlt.faq} />
      <ProductHero title={t.railwayAlt.heroTitle} subtitle={t.railwayAlt.heroSubtitle} />
      <FeatureGrid title={t.railwayAlt.featuresTitle} features={t.railwayAlt.features} />
      <FaqList title={t.railwayAlt.faqTitle} items={t.railwayAlt.faq} />
      <CtaBand />
    </>
  );
}

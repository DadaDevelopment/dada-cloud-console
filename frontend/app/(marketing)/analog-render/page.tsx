"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

export default function RenderAlternativePage() {
  const { t } = useLang();
  return (
    <>
      <FaqJsonLd path="/analog-render" items={t.renderAlt.faq} />
      <ProductHero title={t.renderAlt.heroTitle} subtitle={t.renderAlt.heroSubtitle} />
      <FeatureGrid title={t.renderAlt.featuresTitle} features={t.renderAlt.features} />
      <FaqList title={t.renderAlt.faqTitle} items={t.renderAlt.faq} />
      <CtaBand />
    </>
  );
}

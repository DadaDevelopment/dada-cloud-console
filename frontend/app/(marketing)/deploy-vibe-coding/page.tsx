"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

export default function VibeCodingDeployPage() {
  const { t } = useLang();
  return (
    <>
      <FaqJsonLd path="/deploy-vibe-coding" items={t.vibeCodingAlt.faq} />
      <ProductHero title={t.vibeCodingAlt.heroTitle} subtitle={t.vibeCodingAlt.heroSubtitle} />
      <FeatureGrid title={t.vibeCodingAlt.featuresTitle} features={t.vibeCodingAlt.features} />
      <FaqList title={t.vibeCodingAlt.faqTitle} items={t.vibeCodingAlt.faq} />
      <CtaBand />
    </>
  );
}

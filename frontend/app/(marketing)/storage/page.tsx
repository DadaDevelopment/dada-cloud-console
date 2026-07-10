"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

export default function StoragePage() {
  const { t } = useLang();
  return (
    <>
      <FaqJsonLd path="/storage" items={t.storage.faq} />
      <ProductHero title={t.storage.heroTitle} subtitle={t.storage.heroSubtitle} badge={t.common.beta} />
      <FeatureGrid title={t.storage.featuresTitle} features={t.storage.features} />
      <FaqList title={t.storage.faqTitle} items={t.storage.faq} />
      <CtaBand />
    </>
  );
}

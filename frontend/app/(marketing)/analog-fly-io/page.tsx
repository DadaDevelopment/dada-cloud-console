"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

export default function FlyIoAlternativePage() {
  const { t } = useLang();
  return (
    <>
      <FaqJsonLd path="/analog-fly-io" items={t.flyIoAlt.faq} />
      <ProductHero title={t.flyIoAlt.heroTitle} subtitle={t.flyIoAlt.heroSubtitle} />
      <FeatureGrid title={t.flyIoAlt.featuresTitle} features={t.flyIoAlt.features} />
      <FaqList title={t.flyIoAlt.faqTitle} items={t.flyIoAlt.faq} />
      <CtaBand />
    </>
  );
}

"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

export default function VercelAlternativePage() {
  const { t } = useLang();
  return (
    <>
      <FaqJsonLd path="/analog-vercel" items={t.vercelAlt.faq} />
      <ProductHero title={t.vercelAlt.heroTitle} subtitle={t.vercelAlt.heroSubtitle} />
      <FeatureGrid title={t.vercelAlt.featuresTitle} features={t.vercelAlt.features} />
      <FaqList title={t.vercelAlt.faqTitle} items={t.vercelAlt.faq} />
      <CtaBand />
    </>
  );
}

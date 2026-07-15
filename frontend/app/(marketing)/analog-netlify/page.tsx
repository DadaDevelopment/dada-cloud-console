"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

export default function NetlifyAlternativePage() {
  const { t } = useLang();
  return (
    <>
      <FaqJsonLd path="/analog-netlify" items={t.netlifyAlt.faq} />
      <ProductHero title={t.netlifyAlt.heroTitle} subtitle={t.netlifyAlt.heroSubtitle} />
      <FeatureGrid title={t.netlifyAlt.featuresTitle} features={t.netlifyAlt.features} />
      <FaqList title={t.netlifyAlt.faqTitle} items={t.netlifyAlt.faq} />
      <CtaBand />
    </>
  );
}

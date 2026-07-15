"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

export default function HerokuAlternativePage() {
  const { t } = useLang();
  return (
    <>
      <FaqJsonLd path="/analog-heroku" items={t.herokuAlt.faq} />
      <ProductHero title={t.herokuAlt.heroTitle} subtitle={t.herokuAlt.heroSubtitle} />
      <FeatureGrid title={t.herokuAlt.featuresTitle} features={t.herokuAlt.features} />
      <FaqList title={t.herokuAlt.faqTitle} items={t.herokuAlt.faq} />
      <CtaBand />
    </>
  );
}

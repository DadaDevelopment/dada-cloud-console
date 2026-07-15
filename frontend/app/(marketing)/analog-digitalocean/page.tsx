"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

export default function DigitalOceanAlternativePage() {
  const { t } = useLang();
  return (
    <>
      <FaqJsonLd path="/analog-digitalocean" items={t.digitaloceanAlt.faq} />
      <ProductHero title={t.digitaloceanAlt.heroTitle} subtitle={t.digitaloceanAlt.heroSubtitle} />
      <FeatureGrid title={t.digitaloceanAlt.featuresTitle} features={t.digitaloceanAlt.features} />
      <FaqList title={t.digitaloceanAlt.faqTitle} items={t.digitaloceanAlt.faq} />
      <CtaBand />
    </>
  );
}

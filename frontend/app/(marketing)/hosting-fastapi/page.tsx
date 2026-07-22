"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

export default function FastapiHostingPage() {
  const { t } = useLang();
  return (
    <>
      <FaqJsonLd path="/hosting-fastapi" items={t.fastapiAlt.faq} />
      <ProductHero
        title={t.fastapiAlt.heroTitle}
        subtitle={t.fastapiAlt.heroSubtitle}
        ctaHref="/register?utm_source=pseo_fastapi"
      />
      <FeatureGrid title={t.fastapiAlt.featuresTitle} features={t.fastapiAlt.features} />
      <FaqList title={t.fastapiAlt.faqTitle} items={t.fastapiAlt.faq} />
      <CtaBand ctaHref="/register?utm_source=pseo_fastapi" />
    </>
  );
}

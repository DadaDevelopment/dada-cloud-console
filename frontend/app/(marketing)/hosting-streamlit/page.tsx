"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

export default function StreamlitHostingPage() {
  const { t } = useLang();
  return (
    <>
      <FaqJsonLd path="/hosting-streamlit" items={t.streamlitAlt.faq} />
      <ProductHero
        title={t.streamlitAlt.heroTitle}
        subtitle={t.streamlitAlt.heroSubtitle}
        ctaHref="/register?utm_source=pseo_streamlit"
      />
      <FeatureGrid title={t.streamlitAlt.featuresTitle} features={t.streamlitAlt.features} />
      <FaqList title={t.streamlitAlt.faqTitle} items={t.streamlitAlt.faq} />
      <CtaBand ctaHref="/register?utm_source=pseo_streamlit" />
    </>
  );
}

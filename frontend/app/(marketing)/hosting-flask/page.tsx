"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

export default function FlaskHostingPage() {
  const { t } = useLang();
  return (
    <>
      <FaqJsonLd path="/hosting-flask" items={t.flaskAlt.faq} />
      <ProductHero
        title={t.flaskAlt.heroTitle}
        subtitle={t.flaskAlt.heroSubtitle}
        ctaHref="/register?utm_source=pseo_flask"
      />
      <FeatureGrid title={t.flaskAlt.featuresTitle} features={t.flaskAlt.features} />
      <FaqList title={t.flaskAlt.faqTitle} items={t.flaskAlt.faq} />
      <CtaBand ctaHref="/register?utm_source=pseo_flask" />
    </>
  );
}

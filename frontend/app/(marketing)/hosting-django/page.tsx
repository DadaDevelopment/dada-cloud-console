"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

export default function DjangoHostingPage() {
  const { t } = useLang();
  return (
    <>
      <FaqJsonLd path="/hosting-django" items={t.djangoAlt.faq} />
      <ProductHero
        title={t.djangoAlt.heroTitle}
        subtitle={t.djangoAlt.heroSubtitle}
        ctaHref="/register?utm_source=pseo_django"
      />
      <FeatureGrid title={t.djangoAlt.featuresTitle} features={t.djangoAlt.features} />
      <FaqList title={t.djangoAlt.faqTitle} items={t.djangoAlt.faq} />
      <CtaBand ctaHref="/register?utm_source=pseo_django" />
    </>
  );
}

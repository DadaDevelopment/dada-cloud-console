"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

export default function CloudServersPage() {
  const { t } = useLang();
  return (
    <>
      <FaqJsonLd path="/cloud-servers" items={t.servers.faq} />
      <ProductHero title={t.servers.heroTitle} subtitle={t.servers.heroSubtitle} />
      <FeatureGrid title={t.servers.featuresTitle} features={t.servers.features} />
      <FaqList title={t.servers.faqTitle} items={t.servers.faq} />
      <CtaBand />
    </>
  );
}

"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";

export default function CloudServersPage() {
  const { t } = useLang();
  return (
    <>
      <ProductHero title={t.servers.heroTitle} subtitle={t.servers.heroSubtitle} />
      <FeatureGrid features={t.servers.features} />
      <FaqList title={t.servers.faqTitle} items={t.servers.faq} />
      <CtaBand />
    </>
  );
}

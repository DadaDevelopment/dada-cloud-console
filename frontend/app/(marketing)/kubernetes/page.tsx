"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";

export default function KubernetesPage() {
  const { t } = useLang();
  return (
    <>
      <ProductHero title={t.kubernetes.heroTitle} subtitle={t.kubernetes.heroSubtitle} badge={t.common.beta} />
      <FeatureGrid features={t.kubernetes.features} />
      <FaqList title={t.kubernetes.faqTitle} items={t.kubernetes.faq} />
      <CtaBand />
    </>
  );
}

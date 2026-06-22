"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, CtaBand } from "@/components/marketing/sections";

export default function StoragePage() {
  const { t } = useLang();
  return (
    <>
      <ProductHero title={t.storage.heroTitle} subtitle={t.storage.heroSubtitle} />
      <FeatureGrid features={t.storage.features} />
      <CtaBand />
    </>
  );
}

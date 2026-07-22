"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

export default function VkBotHostingPage() {
  const { t } = useLang();
  return (
    <>
      <FaqJsonLd path="/hosting-vk-bot" items={t.vkBotAlt.faq} />
      <ProductHero
        title={t.vkBotAlt.heroTitle}
        subtitle={t.vkBotAlt.heroSubtitle}
        ctaHref="/register?utm_source=pseo_vk_bot"
      />
      <FeatureGrid title={t.vkBotAlt.featuresTitle} features={t.vkBotAlt.features} />
      <FaqList title={t.vkBotAlt.faqTitle} items={t.vkBotAlt.faq} />
      <CtaBand ctaHref="/register?utm_source=pseo_vk_bot" />
    </>
  );
}

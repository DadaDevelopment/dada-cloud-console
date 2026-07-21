"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

export default function TelegramBotHostingPage() {
  const { t } = useLang();
  return (
    <>
      <FaqJsonLd path="/hosting-telegram-bot" items={t.telegramBotAlt.faq} />
      <ProductHero
        title={t.telegramBotAlt.heroTitle}
        subtitle={t.telegramBotAlt.heroSubtitle}
        ctaHref="/register?utm_source=door_a"
      />
      <FeatureGrid title={t.telegramBotAlt.featuresTitle} features={t.telegramBotAlt.features} />
      <FaqList title={t.telegramBotAlt.faqTitle} items={t.telegramBotAlt.faq} />
      <CtaBand ctaHref="/register?utm_source=door_a" />
    </>
  );
}

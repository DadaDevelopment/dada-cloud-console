"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, StepsGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";
import { HowToJsonLd } from "@/components/marketing/howto-jsonld";

const UTM = "utm_source=telegram-agent-gateway";

export default function TelegramAgentGatewayPage() {
  const { t } = useLang();
  const g = t.tgGatewayAlt;

  return (
    <>
      <FaqJsonLd path="/telegram-agent-gateway" items={g.faq} />
      {g.howtoSteps && g.howtoSteps.length > 0 && (
        <HowToJsonLd
          path="/telegram-agent-gateway"
          name={g.howtoTitle ?? g.heroTitle}
          description={g.howtoSubtitle ?? g.heroSubtitle}
          steps={g.howtoSteps.map((s) => ({ name: s.title, text: s.desc }))}
        />
      )}
      <ProductHero title={g.heroTitle} subtitle={g.heroSubtitle} ctaHref={`/register?${UTM}`} />
      <FeatureGrid title={g.featuresTitle} features={g.features} />
      {g.howtoSteps && g.howtoSteps.length > 0 && (
        <StepsGrid title={g.howtoTitle ?? ""} subtitle={g.howtoSubtitle} steps={g.howtoSteps} />
      )}
      <FaqList title={g.faqTitle} items={g.faq} />
      <CtaBand ctaHref={`/register?${UTM}`} />
    </>
  );
}

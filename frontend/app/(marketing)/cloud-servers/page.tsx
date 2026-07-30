"use client";

import { useLang } from "@/lib/i18n/context";
import {
  ProductHero,
  FeatureGrid,
  StepsGrid,
  UseCaseGrid,
  FaqList,
  CtaBand,
  RelatedLinks,
} from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";
import { HowToJsonLd } from "@/components/marketing/howto-jsonld";

export default function CloudServersPage() {
  const { t } = useLang();
  return (
    <>
      <FaqJsonLd path="/cloud-servers" items={t.servers.faq} />
      <HowToJsonLd
        path="/cloud-servers"
        name={t.servers.howtoTitle}
        description={t.servers.howtoSubtitle}
        steps={t.servers.howtoSteps.map((s) => ({ name: s.title, text: s.desc }))}
      />
      <ProductHero title={t.servers.heroTitle} subtitle={t.servers.heroSubtitle} />
      <FeatureGrid title={t.servers.featuresTitle} features={t.servers.features} />
      <StepsGrid
        title={t.servers.howtoTitle}
        subtitle={t.servers.howtoSubtitle}
        steps={t.servers.howtoSteps}
      />
      <UseCaseGrid
        title={t.servers.useCasesTitle}
        subtitle={t.servers.useCasesSubtitle}
        items={t.servers.useCases}
      />
      <UseCaseGrid
        title={t.servers.limitsTitle}
        subtitle={t.servers.limitsSubtitle}
        items={t.servers.limits}
      />
      <FaqList title={t.servers.faqTitle} items={t.servers.faq} />
      <RelatedLinks
        links={[
          { label: t.databases.heroTitle, href: "/databases" },
          { label: t.storage.heroTitle, href: "/storage" },
          { label: t.pricing.heroTitle, href: "/pricing" },
        ]}
      />
      <CtaBand />
    </>
  );
}

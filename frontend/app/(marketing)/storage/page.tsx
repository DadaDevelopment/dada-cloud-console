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

export default function StoragePage() {
  const { t, locale } = useLang();
  return (
    <>
      <FaqJsonLd path="/storage" items={t.storage.faq} />
      <HowToJsonLd
        path="/storage"
        name={t.storage.howtoTitle}
        description={t.storage.howtoSubtitle}
        steps={t.storage.howtoSteps.map((s) => ({ name: s.title, text: s.desc }))}
      />
      <ProductHero title={t.storage.heroTitle} subtitle={t.storage.heroSubtitle} badge={t.common.beta} />
      <FeatureGrid title={t.storage.featuresTitle} features={t.storage.features} />
      <StepsGrid
        title={t.storage.howtoTitle}
        subtitle={t.storage.howtoSubtitle}
        steps={t.storage.howtoSteps}
      />
      <UseCaseGrid
        title={t.storage.useCasesTitle}
        subtitle={t.storage.useCasesSubtitle}
        items={t.storage.useCases}
      />
      <UseCaseGrid
        title={t.storage.limitsTitle}
        subtitle={t.storage.limitsSubtitle}
        items={t.storage.limits}
      />
      <FaqList title={t.storage.faqTitle} items={t.storage.faq} />
      <RelatedLinks
        links={[
          {
            label: locale === "en" ? "Guide: object storage (S3)" : "Руководство: объектное хранилище S3",
            href: "/developer/object-storage",
          },
          { label: t.databases.heroTitle, href: "/databases" },
          { label: t.servers.heroTitle, href: "/cloud-servers" },
          { label: t.pricing.heroTitle, href: "/pricing" },
        ]}
      />
      <CtaBand />
    </>
  );
}

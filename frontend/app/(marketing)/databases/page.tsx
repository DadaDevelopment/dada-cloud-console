"use client";

import Link from "next/link";
import { useLang } from "@/lib/i18n/context";
import { localeHref } from "@/lib/site";
import {
  ProductHero,
  FeatureGrid,
  StepsGrid,
  UseCaseGrid,
  FaqList,
  PillList,
  CtaBand,
  RelatedLinks,
} from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";
import { HowToJsonLd } from "@/components/marketing/howto-jsonld";

export default function DatabasesPage() {
  const { t, locale } = useLang();
  return (
    <>
      <FaqJsonLd path="/databases" items={t.databases.faq} />
      <HowToJsonLd
        path="/databases"
        name={t.databases.howtoTitle}
        description={t.databases.howtoSubtitle}
        steps={t.databases.howtoSteps.map((s) => ({ name: s.title, text: s.desc }))}
      />
      <ProductHero title={t.databases.heroTitle} subtitle={t.databases.heroSubtitle} />
      <section className="bg-white pt-16">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <PillList items={t.databases.engines} />
          <p className="mt-4 text-sm text-slate-500">
            {t.databases.note}{" "}
            <Link href={localeHref("/cloud-servers", locale)} className="font-medium text-blue-600 hover:text-blue-700">
              {t.databases.noteLinkLabel}
            </Link>
          </p>
        </div>
      </section>
      <FeatureGrid title={t.databases.featuresTitle} features={t.databases.features} />
      <StepsGrid
        title={t.databases.howtoTitle}
        subtitle={t.databases.howtoSubtitle}
        steps={t.databases.howtoSteps}
      />
      <UseCaseGrid
        title={t.databases.useCasesTitle}
        subtitle={t.databases.useCasesSubtitle}
        items={t.databases.useCases}
      />
      <UseCaseGrid
        title={t.databases.limitsTitle}
        subtitle={t.databases.limitsSubtitle}
        items={t.databases.limits}
      />
      <FaqList title={t.databases.faqTitle} items={t.databases.faq} />
      <RelatedLinks
        links={[
          {
            label: locale === "en" ? "Guide: managed PostgreSQL" : "Руководство: управляемый PostgreSQL",
            href: "/developer/databases-postgres",
          },
          { label: t.storage.heroTitle, href: "/storage" },
          { label: t.servers.heroTitle, href: "/cloud-servers" },
          { label: t.pricing.heroTitle, href: "/pricing" },
        ]}
      />
      <CtaBand />
    </>
  );
}

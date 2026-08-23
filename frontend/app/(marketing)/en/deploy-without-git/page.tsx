"use client";

import { useLang } from "@/lib/i18n/context";
import { ProductHero, FeatureGrid, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";
import { HowToJsonLd } from "@/components/marketing/howto-jsonld";
import { PricingAndCaveats } from "@/components/marketing/alt-landing";

const UTM = "utm_source=upload_landing";

export default function DeployWithoutGitPage() {
  const { t } = useLang();
  const g = t.uploadDeployAlt;

  return (
    <>
      <FaqJsonLd path="/deploy-without-git" items={g.faq} />
      <HowToJsonLd
        path="/deploy-without-git"
        name={g.heroTitle}
        description={g.heroSubtitle}
        steps={g.steps.map((s) => ({ name: s.title, text: s.desc }))}
      />
      <ProductHero title={g.heroTitle} subtitle={g.heroSubtitle} ctaHref={`/login?${UTM}`} />

      <section className="bg-white py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="mb-12 max-w-2xl">
            <h2 className="text-3xl font-bold tracking-tight text-slate-900">{g.stepsTitle}</h2>
            <p className="mt-3 text-lg text-slate-600">{g.stepsSubtitle}</p>
          </div>
          <div className="grid gap-6 md:grid-cols-4">
            {g.steps.map((s) => (
              <div key={s.num} id={`step-${s.num}`} className="relative rounded-xl border border-slate-200 bg-white p-7">
                <span className="absolute right-6 top-6 text-4xl font-bold text-slate-100">{s.num}</span>
                <h3 className="text-lg font-semibold text-slate-900">{s.title}</h3>
                <p className="mt-2 text-sm text-slate-600">{s.desc}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      <FeatureGrid title={g.featuresTitle} features={g.features} />
      <PricingAndCaveats {...g} />
      <FaqList title={g.faqTitle} items={g.faq} />
      <CtaBand ctaHref={`/login?${UTM}`} />
    </>
  );
}

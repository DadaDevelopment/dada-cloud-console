"use client";

import { ProductHero, FeatureGrid, FaqList, CtaBand, RelatedLinks } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";
import { HowToJsonLd } from "@/components/marketing/howto-jsonld";

type Feature = { title: string; desc: string };
type Step = { num: string; title: string; desc: string };
type MappingRow = { from: string; to: string; note: string };

export type GuideCopy = {
  heroTitle: string;
  heroSubtitle: string;
  featuresTitle: string;
  features: Feature[];
  faqTitle: string;
  faq: { q: string; a: string }[];
  howtoTitle?: string;
  howtoSubtitle?: string;
  howtoSteps?: Step[];
  mappingTitle?: string;
  mappingSubtitle?: string;
  mapping?: MappingRow[];
  mappingHeaders?: [string, string, string];
  notPortableTitle?: string;
  notPortableSubtitle?: string;
  notPortable?: Feature[];
};

/**
 * Long-form landing renderer shared by the intent-guide pages (payment blockers,
 * availability checks). Same section order as the `/analog-*` cluster, plus a
 * related-links strip so no guide page ends up an orphan in the internal graph.
 */
export function LandingGuide({
  path,
  utm,
  copy,
  related,
}: {
  path: string;
  utm: string;
  copy: GuideCopy;
  related?: { label: string; href: string }[];
}) {
  const g = copy;
  const cta = `/register?${utm}`;

  return (
    <>
      <FaqJsonLd path={path} items={g.faq} />
      {g.howtoSteps && g.howtoSteps.length > 0 && (
        <HowToJsonLd
          path={path}
          name={g.howtoTitle ?? g.heroTitle}
          description={g.howtoSubtitle ?? g.heroSubtitle}
          steps={g.howtoSteps.map((s) => ({ name: s.title, text: s.desc }))}
        />
      )}
      <ProductHero title={g.heroTitle} subtitle={g.heroSubtitle} ctaHref={cta} />
      <FeatureGrid title={g.featuresTitle} features={g.features} />

      {g.howtoSteps && g.howtoSteps.length > 0 && (
        <section className="bg-white py-20">
          <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
            <div className="mb-12 max-w-2xl">
              <h2 className="text-3xl font-bold tracking-tight text-slate-900">{g.howtoTitle}</h2>
              <p className="mt-3 text-lg text-slate-600">{g.howtoSubtitle}</p>
            </div>
            <div className="grid gap-6 md:grid-cols-4">
              {g.howtoSteps.map((s) => (
                <div key={s.num} id={`step-${s.num}`} className="relative rounded-xl border border-slate-200 bg-white p-7">
                  <span className="absolute right-6 top-6 text-4xl font-bold text-slate-100">{s.num}</span>
                  <h3 className="text-lg font-semibold text-slate-900">{s.title}</h3>
                  <p className="mt-2 text-sm text-slate-600">{s.desc}</p>
                </div>
              ))}
            </div>
          </div>
        </section>
      )}

      {g.mapping && g.mapping.length > 0 && (
        <section className="bg-slate-50 py-20">
          <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
            <div className="mb-10 max-w-2xl">
              <h2 className="text-3xl font-bold tracking-tight text-slate-900">{g.mappingTitle}</h2>
              <p className="mt-3 text-lg text-slate-600">{g.mappingSubtitle}</p>
            </div>
            <div className="overflow-x-auto rounded-xl border border-slate-200 bg-white">
              <table className="w-full min-w-[640px] text-left text-sm">
                <thead className="border-b border-slate-200 bg-slate-50 text-xs font-semibold uppercase tracking-wide text-slate-500">
                  <tr>
                    {(g.mappingHeaders ?? ["", "", ""]).map((h, i) => (
                      <th key={i} className="px-6 py-3">
                        {h}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-200">
                  {g.mapping.map((row) => (
                    <tr key={row.from}>
                      <td className="px-6 py-4 align-top font-medium text-slate-900">{row.from}</td>
                      <td className="px-6 py-4 align-top text-slate-700">{row.to}</td>
                      <td className="px-6 py-4 align-top text-slate-500">{row.note}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            {g.notPortable && g.notPortable.length > 0 && (
              <div className="mt-12">
                <h3 className="text-xl font-bold tracking-tight text-slate-900">{g.notPortableTitle}</h3>
                {g.notPortableSubtitle && <p className="mt-2 text-sm text-slate-600">{g.notPortableSubtitle}</p>}
                <div className="mt-6 grid gap-6 sm:grid-cols-2">
                  {g.notPortable.map((n) => (
                    <div key={n.title} className="rounded-xl border border-amber-200 bg-amber-50 p-6">
                      <h4 className="text-base font-semibold text-slate-900">{n.title}</h4>
                      <p className="mt-2 text-sm text-slate-600">{n.desc}</p>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        </section>
      )}

      <FaqList title={g.faqTitle} items={g.faq} />

      {related && <RelatedLinks links={related} />}

      <CtaBand ctaHref={cta} />
    </>
  );
}

"use client";

import Link from "next/link";
import { useLang } from "@/lib/i18n/context";
import { ProductHero, FaqList } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";
import { HowToJsonLd } from "@/components/marketing/howto-jsonld";
import { consoleHref } from "@/lib/site";

const UTM = "utm_source=migrate-vercel";

export default function MigrateVercelPage() {
  const { t } = useLang();
  const g = t.migrateVercel;

  return (
    <>
      <FaqJsonLd path="/migrate-vercel" items={g.faq} />
      <HowToJsonLd
        path="/migrate-vercel"
        name={g.heroTitle}
        description={g.heroSubtitle}
        steps={g.steps.map((s) => ({ name: s.title, text: s.desc }))}
      />
      <ProductHero title={g.heroTitle} subtitle={g.heroSubtitle} />

      <section className="bg-white py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="mb-12 max-w-2xl">
            <h2 className="text-3xl font-bold tracking-tight text-slate-900">{g.stepsTitle}</h2>
            <p className="mt-3 text-lg text-slate-600">{g.stepsSubtitle}</p>
          </div>
          <div className="grid gap-6 md:grid-cols-3">
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
                  <th className="px-6 py-3">Vercel</th>
                  <th className="px-6 py-3">Dada Cloud</th>
                  <th className="px-6 py-3">Note</th>
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

          <div className="mt-12">
            <h3 className="text-xl font-bold tracking-tight text-slate-900">{g.notesTitle}</h3>
            <div className="mt-6 grid gap-6 sm:grid-cols-2">
              {g.notes.map((n) => (
                <div key={n.title} className="rounded-xl border border-amber-200 bg-amber-50 p-6">
                  <h4 className="text-base font-semibold text-slate-900">{n.title}</h4>
                  <p className="mt-2 text-sm text-slate-600">{n.desc}</p>
                </div>
              ))}
            </div>
          </div>
        </div>
      </section>

      <FaqList title={g.faqTitle} items={g.faq} />

      <section className="mkt-hero-gradient">
        <div className="mx-auto max-w-7xl px-4 py-16 text-center sm:px-6 lg:px-8">
          <h2 className="text-3xl font-bold tracking-tight text-white sm:text-4xl">{g.ctaTitle}</h2>
          <p className="mx-auto mt-4 max-w-xl text-lg text-white/70">{g.ctaSubtitle}</p>
          <Link
            href={`${consoleHref("/register")}?${UTM}`}
            className="mt-8 inline-block rounded-md bg-blue-600 px-8 py-3 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
          >
            {g.ctaButton}
          </Link>
        </div>
      </section>
    </>
  );
}

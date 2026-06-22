"use client";

import Link from "next/link";
import { Check } from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { ProductHero, CtaBand } from "@/components/marketing/sections";
import { clsx } from "clsx";

export default function PricingPage() {
  const { t } = useLang();
  return (
    <>
      <ProductHero title={t.pricing.heroTitle} subtitle={t.pricing.heroSubtitle} />
      <section className="bg-white py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="grid gap-6 lg:grid-cols-3">
            {t.pricing.plans.map((p) => (
              <div
                key={p.key}
                className={clsx(
                  "flex flex-col rounded-2xl border bg-white p-8",
                  p.highlight
                    ? "border-blue-600 shadow-xl ring-1 ring-blue-600"
                    : "border-slate-200",
                )}
              >
                {p.highlight && (
                  <span className="mb-4 inline-block w-fit rounded-full bg-blue-600 px-3 py-1 text-xs font-semibold text-white">
                    Popular
                  </span>
                )}
                <h3 className="text-lg font-semibold text-slate-900">{p.name}</h3>
                <p className="mt-1 text-sm text-slate-500">{p.tagline}</p>
                <div className="mt-5 flex items-baseline gap-1">
                  <span className="text-3xl font-bold text-slate-900">{p.price}</span>
                  {p.period && <span className="text-sm text-slate-500">{p.period}</span>}
                </div>
                <ul className="mt-6 flex-1 space-y-3">
                  {p.features.map((f) => (
                    <li key={f} className="flex items-start gap-2 text-sm text-slate-700">
                      <Check className="mt-0.5 h-4 w-4 shrink-0 text-blue-600" />
                      <span>{f}</span>
                    </li>
                  ))}
                </ul>
                <Link
                  href="/login"
                  className={clsx(
                    "mt-8 rounded-md px-6 py-3 text-center text-sm font-semibold transition-colors",
                    p.highlight
                      ? "bg-blue-600 text-white hover:bg-blue-700"
                      : "border border-slate-200 text-slate-900 hover:bg-slate-50",
                  )}
                >
                  {p.cta}
                </Link>
              </div>
            ))}
          </div>
          <p className="mt-8 text-center text-xs text-slate-400">{t.pricing.note}</p>
        </div>
      </section>
      <CtaBand />
    </>
  );
}

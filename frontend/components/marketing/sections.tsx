"use client";

import Link from "next/link";
import { Check, ChevronDown } from "lucide-react";
import { useState } from "react";
import { useLang } from "@/lib/i18n/context";
import { consoleHref, localeHref } from "@/lib/site";
import { GOAL_LANDING_CTA, ctaSource, reachGoal } from "@/lib/metrika";
import { clsx } from "clsx";

export function ProductHero({
  title,
  subtitle,
  badge,
  ctaHref,
}: {
  title: string;
  subtitle: string;
  badge?: string;
  ctaHref?: string;
}) {
  const { t, locale } = useLang();
  return (
    <section className="mkt-hero-gradient">
      <div className="mkt-grid-bg">
        <div className="mx-auto max-w-7xl px-4 py-20 sm:px-6 lg:px-8 lg:py-28">
          {badge && (
            <span className="mb-4 inline-block rounded-full border border-blue-400/30 bg-blue-500/10 px-3 py-1 text-xs font-semibold uppercase tracking-wide text-blue-300">
              {badge}
            </span>
          )}
          <h1 className="max-w-3xl text-4xl font-bold tracking-tight text-white sm:text-5xl">
            {title}
          </h1>
          <p className="mt-5 max-w-2xl text-lg text-white/70">{subtitle}</p>
          <div className="mt-8 flex flex-wrap gap-3">
            <Link
              href={consoleHref(ctaHref ?? "/register")}
              onClick={() => reachGoal(GOAL_LANDING_CTA, { source: ctaSource(ctaHref ?? ""), placement: "hero" })}
              className="rounded-md bg-blue-600 px-6 py-3 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
            >
              {t.common.createAccount}
            </Link>
            <Link
              href={localeHref("/pricing", locale)}
              className="rounded-md border border-white/20 px-6 py-3 text-sm font-semibold text-white transition-colors hover:bg-white/5"
            >
              {t.common.learnMore}
            </Link>
          </div>
        </div>
      </div>
    </section>
  );
}

export function FeatureGrid({
  title,
  features,
}: {
  title?: string;
  features: { title: string; desc: string }[];
}) {
  return (
    <section className="bg-white py-20">
      <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
        {title && (
          <h2 className="mb-12 text-3xl font-bold tracking-tight text-slate-900">{title}</h2>
        )}
        <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
          {features.map((f) => (
            <div
              key={f.title}
              className="rounded-xl border border-slate-200 bg-white p-6 transition-shadow hover:shadow-md"
            >
              <div className="mb-4 flex h-10 w-10 items-center justify-center rounded-lg bg-blue-50 text-blue-600">
                <Check className="h-5 w-5" />
              </div>
              <h3 className="text-base font-semibold text-slate-900">{f.title}</h3>
              <p className="mt-2 text-sm text-slate-600">{f.desc}</p>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

export function FaqList({ title, items }: { title: string; items: { q: string; a: string }[] }) {
  const [open, setOpen] = useState<number | null>(0);
  return (
    <section className="bg-slate-50 py-20">
      <div className="mx-auto max-w-3xl px-4 sm:px-6 lg:px-8">
        <h2 className="mb-10 text-3xl font-bold tracking-tight text-slate-900">{title}</h2>
        <div className="divide-y divide-slate-200 overflow-hidden rounded-xl border border-slate-200 bg-white">
          {items.map((it, i) => (
            <div key={it.q}>
              <button
                type="button"
                className="flex w-full items-center justify-between gap-4 px-6 py-4 text-left"
                onClick={() => setOpen(open === i ? null : i)}
                aria-expanded={open === i}
              >
                <span className="text-sm font-semibold text-slate-900">{it.q}</span>
                <ChevronDown
                  className={clsx(
                    "h-5 w-5 shrink-0 text-slate-400 transition-transform",
                    open === i && "rotate-180",
                  )}
                />
              </button>
              {open === i && <p className="px-6 pb-4 text-sm text-slate-600">{it.a}</p>}
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

export function CtaBand({ ctaHref }: { ctaHref?: string } = {}) {
  const { t } = useLang();
  return (
    <section className="mkt-hero-gradient">
      <div className="mx-auto max-w-7xl px-4 py-16 text-center sm:px-6 lg:px-8">
        <h2 className="text-3xl font-bold tracking-tight text-white sm:text-4xl">
          {t.home.ctaTitle}
        </h2>
        <p className="mx-auto mt-4 max-w-xl text-lg text-white/70">{t.home.ctaSubtitle}</p>
        <Link
          href={consoleHref(ctaHref ?? "/register")}
          onClick={() => reachGoal(GOAL_LANDING_CTA, { source: ctaSource(ctaHref ?? ""), placement: "band" })}
          className="mt-8 inline-block rounded-md bg-blue-600 px-8 py-3 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
        >
          {t.common.createAccount}
        </Link>
      </div>
    </section>
  );
}

export function PillList({ items }: { items: string[] }) {
  return (
    <div className="flex flex-wrap justify-center gap-3">
      {items.map((it) => (
        <span
          key={it}
          className="rounded-full border border-slate-200 bg-white px-5 py-2 text-sm font-semibold text-slate-800 shadow-sm"
        >
          {it}
        </span>
      ))}
    </div>
  );
}

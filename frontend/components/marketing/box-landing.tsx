"use client";

import Link from "next/link";
import { ArrowRight, Box, Check, Database, Gem, Minus, Plug, Rocket, Zap } from "lucide-react";
import { clsx } from "clsx";
import { useLang } from "@/lib/i18n/context";
import { boxCopy } from "@/lib/box-copy";
import { BoxDemo } from "@/components/marketing/box-demo";
import { BoxAccessForm } from "@/components/marketing/box-access-form";
import { FaqList } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

/**
 * Dada Box private-preview landing, rendered at /box (RU) and /en/box (EN).
 *
 * This is a fake-door experiment, not a shipped product page — see
 * docs/product/box-product-brief.md for what it tests and for the honesty rules
 * the copy has to keep (§6). The "what works / what doesn't" section is not
 * filler: it is the reason we can run a door test without burning trust.
 */

const STEP_ICONS = [Zap, Plug, Database, Gem];

export function BoxLanding() {
  const { locale } = useLang();
  const copy = boxCopy[locale];

  return (
    <>
      <FaqJsonLd path="/box" items={copy.faq.items} />

      {/* Hero */}
      <section className="mkt-hero-gradient">
        <div className="mkt-grid-bg">
          <div className="mx-auto max-w-7xl px-4 py-24 sm:px-6 lg:px-8 lg:py-32">
            <span className="mb-5 inline-flex items-center gap-2 rounded-full border border-amber-400/30 bg-amber-500/10 px-3 py-1 text-xs font-semibold uppercase tracking-wide text-amber-300">
              <span className="h-1.5 w-1.5 rounded-full bg-amber-400" />
              {copy.badge}
            </span>
            <h1 className="max-w-4xl text-4xl font-bold leading-tight tracking-tight text-white sm:text-5xl lg:text-6xl">
              {copy.heroTitle}
            </h1>
            <p className="mt-6 max-w-2xl text-lg text-white/70 sm:text-xl">{copy.heroSubtitle}</p>
            <div className="mt-9 flex flex-wrap items-center gap-3">
              <Link
                href="#access"
                className="inline-flex items-center gap-2 rounded-md bg-blue-600 px-7 py-3 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
              >
                <Box className="h-4 w-4" />
                {copy.heroPrimary}
              </Link>
              <Link
                href="#demo"
                className="rounded-md border border-white/20 px-7 py-3 text-sm font-semibold text-white transition-colors hover:bg-white/5"
              >
                {copy.heroSecondary}
              </Link>
            </div>
            <p className="mt-6 text-sm text-white/50">{copy.heroNote}</p>
          </div>
        </div>
      </section>

      {/* Problem */}
      <section className="bg-white py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="mb-12 max-w-2xl">
            <h2 className="text-3xl font-bold tracking-tight text-slate-900">
              {copy.problem.title}
            </h2>
            <p className="mt-3 text-lg text-slate-600">{copy.problem.subtitle}</p>
          </div>
          <div className="grid gap-6 md:grid-cols-3">
            {copy.problem.items.map((item) => (
              <div key={item.title} className="rounded-xl border border-slate-200 bg-white p-7">
                <h3 className="text-lg font-semibold text-slate-900">{item.title}</h3>
                <p className="mt-2 text-sm text-slate-600">{item.desc}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* How it works */}
      <section id="how" className="scroll-mt-20 bg-slate-50 py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="mb-12 max-w-2xl">
            <h2 className="text-3xl font-bold tracking-tight text-slate-900">{copy.how.title}</h2>
            <p className="mt-3 text-lg text-slate-600">{copy.how.subtitle}</p>
          </div>
          <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
            {copy.how.steps.map((step, i) => {
              const Icon = STEP_ICONS[i] ?? Zap;
              return (
                <div
                  key={step.title}
                  className="flex flex-col rounded-xl border border-slate-200 bg-white p-6"
                >
                  <div className="mb-4 flex h-11 w-11 items-center justify-center rounded-xl bg-blue-600 text-white">
                    <Icon className="h-5 w-5" />
                  </div>
                  <code className="mb-3 w-fit rounded bg-slate-100 px-2 py-1 font-mono text-xs text-slate-700">
                    {step.cmd}
                  </code>
                  <h3 className="text-base font-semibold text-slate-900">{step.title}</h3>
                  <p className="mt-2 text-sm text-slate-600">{step.desc}</p>
                </div>
              );
            })}
          </div>
        </div>
      </section>

      {/* Scripted terminal replay */}
      <BoxDemo
        title={copy.demo.title}
        subtitle={copy.demo.subtitle}
        recordingLabel={copy.demo.recordingLabel}
        playLabel={copy.demo.playLabel}
        replayLabel={copy.demo.replayLabel}
        lines={copy.demo.lines}
      />

      {/* Crystallization — the differentiator */}
      <section className="bg-white py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="grid gap-12 lg:grid-cols-2 lg:items-center">
            <div>
              <div className="mb-5 flex h-12 w-12 items-center justify-center rounded-xl bg-blue-600 text-white">
                <Gem className="h-6 w-6" />
              </div>
              <h2 className="text-3xl font-bold tracking-tight text-slate-900">
                {copy.crystal.title}
              </h2>
              <p className="mt-3 text-lg text-slate-600">{copy.crystal.subtitle}</p>
              <p className="mt-5 rounded-lg border-l-4 border-blue-500 bg-blue-50 p-4 text-sm text-slate-700">
                {copy.crystal.note}
              </p>
            </div>
            <div className="rounded-xl border border-slate-200 bg-slate-50 p-7">
              <h3 className="text-sm font-semibold uppercase tracking-wide text-slate-500">
                {copy.crystal.carriedTitle}
              </h3>
              <ul className="mt-5 space-y-3">
                {copy.crystal.carried.map((item) => (
                  <li key={item} className="flex items-start gap-3 text-sm text-slate-800">
                    <Check className="mt-0.5 h-4 w-4 shrink-0 text-blue-600" />
                    <span>{item}</span>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </div>
      </section>

      {/* Objections */}
      <section className="bg-slate-50 py-20">
        <div className="mx-auto max-w-4xl px-4 sm:px-6 lg:px-8">
          <div className="mb-10 max-w-2xl">
            <h2 className="text-3xl font-bold tracking-tight text-slate-900">{copy.vps.title}</h2>
            <p className="mt-3 text-lg text-slate-600">{copy.vps.subtitle}</p>
          </div>
          <div className="space-y-4">
            {copy.vps.rows.map((row) => (
              <div key={row.claim} className="rounded-xl border border-slate-200 bg-white p-6">
                <p className="text-base font-semibold text-slate-900">— {row.claim}</p>
                <p className="mt-2 text-sm text-slate-600">{row.answer}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* Pricing hypothesis */}
      <section className="bg-white py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="mb-12 max-w-2xl">
            <h2 className="text-3xl font-bold tracking-tight text-slate-900">
              {copy.pricing.title}
            </h2>
            <p className="mt-3 text-lg text-slate-600">{copy.pricing.subtitle}</p>
          </div>
          <div className="grid gap-6 md:grid-cols-3">
            {copy.pricing.tiers.map((tier) => (
              <div key={tier.name} className="rounded-xl border border-slate-200 bg-white p-7">
                <h3 className="text-lg font-semibold text-slate-900">{tier.name}</h3>
                <div className="mt-2 text-xl font-bold text-blue-600">{tier.price}</div>
                <p className="mt-3 text-sm text-slate-600">{tier.note}</p>
              </div>
            ))}
          </div>
          <p className="mt-8 max-w-2xl text-sm text-slate-500">{copy.pricing.disclaimer}</p>
        </div>
      </section>

      <FaqList title={copy.faq.title} items={copy.faq.items} />

      {/* Radical honesty — what is real today and what is not */}
      <section className="bg-slate-900 py-20">
        <div className="mx-auto max-w-5xl px-4 sm:px-6 lg:px-8">
          <div className="mb-10 max-w-2xl">
            <h2 className="text-3xl font-bold tracking-tight text-white">{copy.honesty.title}</h2>
            <p className="mt-3 text-lg text-white/60">{copy.honesty.subtitle}</p>
          </div>
          <div className="grid gap-6 md:grid-cols-2">
            <HonestyCard
              tone="works"
              title={copy.honesty.worksTitle}
              items={copy.honesty.works}
            />
            <HonestyCard
              tone="pending"
              title={copy.honesty.notYetTitle}
              items={copy.honesty.notYet}
            />
          </div>
        </div>
      </section>

      <BoxAccessForm copy={copy} locale={locale} />

      {/* Closing nudge back to the door */}
      <section className="mkt-hero-gradient">
        <div className="mx-auto max-w-7xl px-4 py-16 text-center sm:px-6 lg:px-8">
          <h2 className="text-3xl font-bold tracking-tight text-white sm:text-4xl">
            {copy.heroTitle}
          </h2>
          <p className="mx-auto mt-4 max-w-xl text-lg text-white/70">{copy.heroNote}</p>
          <Link
            href="#access"
            className="mt-8 inline-flex items-center gap-2 rounded-md bg-blue-600 px-8 py-3 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
          >
            <Rocket className="h-4 w-4" />
            {copy.heroPrimary}
            <ArrowRight className="h-4 w-4" />
          </Link>
        </div>
      </section>
    </>
  );
}

function HonestyCard({
  tone,
  title,
  items,
}: {
  tone: "works" | "pending";
  title: string;
  items: string[];
}) {
  const works = tone === "works";
  const Icon = works ? Check : Minus;
  return (
    <div
      className={clsx(
        "rounded-xl border p-7",
        works ? "border-emerald-400/30 bg-emerald-500/5" : "border-white/15 bg-white/5",
      )}
    >
      <h3
        className={clsx(
          "text-sm font-semibold uppercase tracking-wide",
          works ? "text-emerald-300" : "text-white/50",
        )}
      >
        {title}
      </h3>
      <ul className="mt-5 space-y-3">
        {items.map((item) => (
          <li
            key={item}
            className={clsx(
              "flex items-start gap-3 text-sm",
              works ? "text-white/85" : "text-white/60",
            )}
          >
            <Icon
              className={clsx(
                "mt-0.5 h-4 w-4 shrink-0",
                works ? "text-emerald-400" : "text-white/40",
              )}
            />
            <span>{item}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

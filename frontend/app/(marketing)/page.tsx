"use client";

import Link from "next/link";
import {
  GitBranch,
  Database,
  Globe,
  RotateCcw,
  ScrollText,
  ArrowRight,
  Check,
} from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { consoleHref, localeHref } from "@/lib/site";
import { CtaBand, FaqList } from "@/components/marketing/sections";
import { HomeJsonLd } from "@/components/marketing/home-jsonld";
import { McpAgentSection } from "@/components/marketing/mcp-agent";
import { BoxSpotlight } from "@/components/marketing/box-spotlight";
import { clsx } from "clsx";

const STEP_ICONS = [GitBranch, Database, RotateCcw];
const VALUE_ICONS = [GitBranch, Globe, ScrollText];

export default function HomePage() {
  const { t, locale } = useLang();

  return (
    <>
      <HomeJsonLd />
      {/* Hero */}
      <section className="mkt-hero-gradient">
        <div className="mkt-grid-bg">
          <div className="mx-auto max-w-7xl px-4 py-24 sm:px-6 lg:px-8 lg:py-32">
            <span className="mb-5 inline-flex items-center gap-2 rounded-full border border-blue-400/30 bg-blue-500/10 px-3 py-1 text-xs font-semibold text-blue-300">
              <span className="h-1.5 w-1.5 rounded-full bg-blue-400" />
              {t.home.heroBadge}
            </span>
            <h1 className="max-w-4xl text-4xl font-bold leading-tight tracking-tight text-white sm:text-5xl lg:text-6xl">
              {t.home.heroTitle}
            </h1>
            <p className="mt-6 max-w-2xl text-lg text-white/70 sm:text-xl">{t.home.heroSubtitle}</p>
            <div className="mt-9 flex flex-wrap gap-3">
              <Link
                href={consoleHref("/register")}
                className="inline-flex items-center gap-2 rounded-md bg-blue-600 px-7 py-3 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
              >
                <GitBranch className="h-4 w-4" />
                {t.home.heroPrimary}
              </Link>
              <Link
                href="#how"
                className="rounded-md border border-white/20 px-7 py-3 text-sm font-semibold text-white transition-colors hover:bg-white/5"
              >
                {t.home.heroSecondary}
              </Link>
              <Link
                href={localeHref("/pricing", locale)}
                className="rounded-md px-7 py-3 text-sm font-semibold text-white/70 transition-colors hover:text-white"
              >
                {t.home.heroTertiary}
              </Link>
            </div>
          </div>
        </div>
      </section>

      {/* Box — new central product, top placement under the hero */}
      <BoxSpotlight />

      {/* How it works */}
      <section id="how" className="scroll-mt-20 bg-white py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="mb-12 max-w-2xl">
            <h2 className="text-3xl font-bold tracking-tight text-slate-900">{t.home.stepsTitle}</h2>
            <p className="mt-3 text-lg text-slate-600">{t.home.stepsSubtitle}</p>
          </div>
          <div className="grid gap-6 md:grid-cols-3">
            {t.home.steps.map((s, i) => {
              const Icon = STEP_ICONS[i] ?? GitBranch;
              return (
                <div key={s.num} className="relative rounded-xl border border-slate-200 bg-white p-7">
                  <span className="absolute right-6 top-6 text-4xl font-bold text-slate-100">
                    {s.num}
                  </span>
                  <div className="mb-4 flex h-12 w-12 items-center justify-center rounded-xl bg-blue-600 text-white">
                    <Icon className="h-6 w-6" />
                  </div>
                  <h3 className="text-lg font-semibold text-slate-900">{s.title}</h3>
                  <p className="mt-2 text-sm text-slate-600">{s.desc}</p>
                </div>
              );
            })}
          </div>
        </div>
      </section>

      {/* Value props */}
      <section className="bg-slate-50 py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="mb-12 max-w-2xl">
            <h2 className="text-3xl font-bold tracking-tight text-slate-900">{t.home.valueTitle}</h2>
            <p className="mt-3 text-lg text-slate-600">{t.home.valueSubtitle}</p>
          </div>
          <div className="grid gap-6 md:grid-cols-3">
            {t.home.value.map((f, i) => {
              const Icon = VALUE_ICONS[i] ?? Check;
              return (
                <div key={f.title} className="rounded-xl border border-slate-200 bg-white p-7">
                  <div className="mb-4 flex h-12 w-12 items-center justify-center rounded-xl bg-blue-50 text-blue-600">
                    <Icon className="h-6 w-6" />
                  </div>
                  <h3 className="text-lg font-semibold text-slate-900">{f.title}</h3>
                  <p className="mt-2 text-sm text-slate-600">{f.desc}</p>
                </div>
              );
            })}
          </div>
        </div>
      </section>

      {/* Scenarios */}
      <section className="bg-white py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="mb-12 max-w-2xl">
            <h2 className="text-3xl font-bold tracking-tight text-slate-900">
              {t.home.scenariosTitle}
            </h2>
            <p className="mt-3 text-lg text-slate-600">{t.home.scenariosSubtitle}</p>
          </div>
          <div className="grid gap-6 md:grid-cols-3">
            {t.home.scenarios.map((s) => (
              <div
                key={s.tag}
                className="flex flex-col rounded-xl border border-slate-200 bg-white p-7 transition-shadow hover:shadow-md"
              >
                <span className="mb-4 inline-flex w-fit rounded-full bg-blue-50 px-3 py-1 text-xs font-semibold text-blue-700">
                  {s.tag}
                </span>
                <h3 className="text-lg font-semibold text-slate-900">{s.title}</h3>
                <p className="mt-2 text-sm text-slate-600">{s.desc}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* MCP / AI agent */}
      <McpAgentSection copy={t.home.mcp} href={localeHref("/developer/mcp-ai-agents", locale)} />

      {/* Social proof */}
      <section className="bg-slate-50 py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="mb-12 max-w-2xl">
            <h2 className="text-3xl font-bold tracking-tight text-slate-900">{t.home.proofTitle}</h2>
            <p className="mt-3 text-lg text-slate-600">{t.home.proofSubtitle}</p>
          </div>
          <div className="grid gap-6 md:grid-cols-3">
            {t.home.proof.map((p) => (
              <figure
                key={p.author}
                className="flex flex-col justify-between rounded-xl border border-slate-200 bg-white p-7"
              >
                <blockquote className="text-base text-slate-800">“{p.quote}”</blockquote>
                <figcaption className="mt-5 text-sm font-medium text-slate-400">
                  {p.author}
                </figcaption>
              </figure>
            ))}
          </div>
        </div>
      </section>

      {/* Pricing teaser */}
      <section className="bg-white py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="mb-12 max-w-2xl">
            <h2 className="text-3xl font-bold tracking-tight text-slate-900">
              {t.home.pricingTitle}
            </h2>
            <p className="mt-3 text-lg text-slate-600">{t.home.pricingSubtitle}</p>
          </div>
          <div className="grid gap-6 md:grid-cols-3">
            {t.home.pricingTiers.map((tier) => (
              <div
                key={tier.name}
                className={clsx(
                  "flex flex-col rounded-xl border bg-white p-7",
                  tier.highlight ? "border-blue-500 shadow-lg" : "border-slate-200",
                )}
              >
                <div className="flex items-center justify-between">
                  <h3 className="text-lg font-semibold text-slate-900">{tier.name}</h3>
                  {tier.highlight && (
                    <span className="rounded-full bg-blue-600 px-2 py-0.5 text-xs font-semibold text-white">
                      {t.common.getStarted}
                    </span>
                  )}
                </div>
                <div className="mt-3 text-2xl font-bold text-slate-900">{tier.price}</div>
                <p className="mt-1 text-sm text-slate-600">{tier.tagline}</p>
                <ul className="mt-5 space-y-2">
                  {tier.bullets.map((b) => (
                    <li key={b} className="flex items-start gap-2 text-sm text-slate-700">
                      <Check className="mt-0.5 h-4 w-4 shrink-0 text-blue-600" />
                      <span>{b}</span>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
          <div className="mt-8 flex flex-wrap items-center gap-4">
            <Link
              href={localeHref("/pricing", locale)}
              className="inline-flex items-center gap-2 rounded-md bg-blue-600 px-6 py-3 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
            >
              {t.common.learnMore}
              <ArrowRight className="h-4 w-4" />
            </Link>
            <p className="text-xs text-slate-400">{t.home.pricingNote}</p>
          </div>
        </div>
      </section>

      {/* FAQ objections */}
      <FaqList title={t.home.faqTitle} items={t.home.faq} />

      {/* Landing hub: every marketing page reachable from the highest-authority page */}
      <section className="bg-slate-50 py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="mb-10 max-w-2xl">
            <h2 className="text-3xl font-bold tracking-tight text-slate-900">{t.home.hubTitle}</h2>
            <p className="mt-3 text-lg text-slate-600">{t.home.hubSubtitle}</p>
          </div>
          <div className="grid gap-10 sm:grid-cols-2">
            {[
              { title: t.footer.productsTitle, links: t.footer.products },
              { title: t.footer.hostingTitle, links: t.footer.hosting },
            ].map((col) => (
              <div key={col.title}>
                <h3 className="text-sm font-semibold uppercase tracking-wide text-slate-500">{col.title}</h3>
                <ul className="mt-4 grid gap-2 sm:grid-cols-2">
                  {col.links
                    .filter((l) => !l.href.startsWith("http"))
                    .map((l) => (
                      <li key={l.href}>
                        <Link
                          href={localeHref(l.href, locale)}
                          className="text-sm text-slate-700 transition-colors hover:text-blue-600"
                        >
                          {l.label}
                        </Link>
                      </li>
                    ))}
                </ul>
              </div>
            ))}
          </div>
        </div>
      </section>

      <CtaBand />
    </>
  );
}

"use client";

import Link from "next/link";
import { ArrowRight, ChevronDown } from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { consoleHref, localeHref } from "@/lib/site";
import { GOAL_LANDING_CTA, ctaSource, reachGoal } from "@/lib/metrika";
import styles from "./sections.module.css";

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
    <section className={styles.hero}>
      <div className={styles.container}>
        {badge && <p className={styles.badge}><span aria-hidden="true" />{badge}</p>}
        <h1 className={styles.heroTitle}>{title}</h1>
        <div className={styles.heroBottom}>
          <p className={styles.heroSubtitle}>{subtitle}</p>
          <div className={styles.actions}>
            <Link
              href={consoleHref(ctaHref ?? "/login")}
              onClick={() => reachGoal(GOAL_LANDING_CTA, { source: ctaSource(ctaHref ?? ""), placement: "hero" })}
              className={styles.primaryButton}
            >
              {t.common.createAccount}<ArrowRight size={17} aria-hidden="true" />
            </Link>
            <Link href={localeHref("/pricing", locale)} className={styles.secondaryButton}>
              {t.nav.pricing}
            </Link>
          </div>
        </div>
      </div>
    </section>
  );
}

export function FeatureGrid({ title, features }: {
  title?: string;
  features: { title: string; desc: string }[];
}) {
  return (
    <section className={styles.section}>
      <div className={styles.container}>
        {title && <h2 className={styles.sectionTitle}>{title}</h2>}
        <div className={styles.featureGrid}>
          {features.map((feature, index) => (
            <article key={feature.title} className={styles.feature}>
              <span className={styles.number} aria-hidden="true">{String(index + 1).padStart(2, "0")}</span>
              <div><h3>{feature.title}</h3><p>{feature.desc}</p></div>
            </article>
          ))}
        </div>
      </div>
    </section>
  );
}

export function StepsGrid({ title, subtitle, steps }: {
  title: string;
  subtitle?: string;
  steps: { num: string; title: string; desc: string }[];
}) {
  return (
    <section className={`${styles.section} ${styles.tinted}`}>
      <div className={styles.container}>
        <div className={styles.sectionIntro}>
          <h2 className={styles.sectionTitle}>{title}</h2>
          {subtitle && <p className={styles.sectionSubtitle}>{subtitle}</p>}
        </div>
        <ol className={styles.steps}>
          {steps.map((step) => (
            <li key={step.num}>
              <span className={styles.stepNumber} aria-hidden="true">{step.num}</span>
              <h3>{step.title}</h3><p>{step.desc}</p>
            </li>
          ))}
        </ol>
      </div>
    </section>
  );
}

export function UseCaseGrid({ title, subtitle, items }: {
  title: string;
  subtitle?: string;
  items: { title: string; desc: string }[];
}) {
  return (
    <section className={styles.section}>
      <div className={styles.container}>
        <div className={styles.sectionIntro}>
          <h2 className={styles.sectionTitle}>{title}</h2>
          {subtitle && <p className={styles.sectionSubtitle}>{subtitle}</p>}
        </div>
        <div className={styles.useCases}>
          {items.map((item) => (
            <article key={item.title}><h3>{item.title}</h3><p>{item.desc}</p></article>
          ))}
        </div>
      </div>
    </section>
  );
}

export function FaqList({ title, items }: { title: string; items: { q: string; a: string }[] }) {
  return (
    <section className={`${styles.section} ${styles.tinted}`}>
      <div className={`${styles.container} ${styles.faqLayout}`}>
        <h2 className={styles.sectionTitle}>{title}</h2>
        <div className={styles.faqList}>
          {items.map((item, index) => (
            <details key={item.q} open={index === 0}>
              <summary>{item.q}<ChevronDown size={20} aria-hidden="true" /></summary>
              <p>{item.a}</p>
            </details>
          ))}
        </div>
      </div>
    </section>
  );
}

export function CtaBand({ ctaHref }: { ctaHref?: string } = {}) {
  const { t } = useLang();
  return (
    <section className={styles.ctaBand}>
      <div className={`${styles.container} ${styles.ctaLayout}`}>
        <div><h2>{t.home.ctaTitle}</h2><p>{t.home.ctaSubtitle}</p></div>
        <Link
          href={consoleHref(ctaHref ?? "/login")}
          onClick={() => reachGoal(GOAL_LANDING_CTA, { source: ctaSource(ctaHref ?? ""), placement: "band" })}
          className={styles.primaryButton}
        >
          {t.common.createAccount}<ArrowRight size={17} aria-hidden="true" />
        </Link>
      </div>
    </section>
  );
}

export function RelatedLinks({ links }: { links: { label: string; href: string }[] }) {
  const { locale } = useLang();
  if (links.length === 0) return null;
  return (
    <section className="border-t border-slate-200 bg-white py-12">
      <div className="mx-auto flex max-w-7xl flex-wrap gap-x-6 gap-y-3 px-4 text-sm sm:px-6 lg:px-8">
        {links.map((r) => (
          <Link
            key={r.href}
            href={localeHref(r.href, locale)}
            className="font-semibold text-blue-600 transition-colors hover:text-blue-700"
          >
            {r.label}
          </Link>
        ))}
      </div>
    </section>
  );
}

export function PayNote({
  title,
  body,
  linkLabel,
  linkHref,
}: {
  title: string;
  body: string;
  linkLabel: string;
  linkHref: string;
}) {
  const { locale } = useLang();
  return (
    <section className="border-t border-slate-200 bg-slate-50 py-12">
      <div className="mx-auto max-w-3xl px-4 sm:px-6 lg:px-8">
        <h2 className="text-xl font-bold tracking-tight text-slate-900">{title}</h2>
        <p className="mt-3 text-base leading-relaxed text-slate-600">{body}</p>
        <Link
          href={localeHref(linkHref, locale)}
          className="mt-4 inline-block text-sm font-semibold text-blue-600 transition-colors hover:text-blue-700"
        >
          {linkLabel} &rarr;
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

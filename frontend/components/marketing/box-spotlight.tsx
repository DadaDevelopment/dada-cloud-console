"use client";

import Link from "next/link";
import { ArrowRight, Box, Check } from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { localeHref } from "@/lib/site";
import { boxCopy } from "@/lib/box-copy";

/**
 * Box promo band for the main marketing page.
 *
 * Box is becoming the central product (see docs/product/box-product-brief.md), so
 * it gets top placement directly under the hero rather than a link buried in the
 * nav. The main hero itself is deliberately left alone: it still carries the
 * revenue-generating funnel, and swapping it should be a decision made on fake-door
 * data, not ahead of it. When that data arrives, this band is what gets promoted.
 */
export function BoxSpotlight() {
  const { locale } = useLang();
  const copy = boxCopy[locale].spotlight;

  return (
    <section className="border-y border-blue-500/20 bg-slate-950 py-14">
      <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
        <div className="grid gap-8 lg:grid-cols-[1.4fr_1fr] lg:items-center">
          <div>
            <span className="mb-4 inline-flex items-center gap-2 rounded-full border border-amber-400/30 bg-amber-500/10 px-3 py-1 text-xs font-semibold uppercase tracking-wide text-amber-300">
              <span className="h-1.5 w-1.5 rounded-full bg-amber-400" />
              {copy.eyebrow}
            </span>
            <h2 className="flex flex-wrap items-center gap-3 text-2xl font-bold tracking-tight text-white sm:text-3xl">
              <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-blue-600">
                <Box className="h-5 w-5" />
              </span>
              {copy.title}
            </h2>
            <p className="mt-4 max-w-2xl text-base text-white/70">{copy.body}</p>
            <Link
              href={localeHref("/box", locale)}
              className="mt-6 inline-flex items-center gap-2 rounded-md bg-blue-600 px-6 py-3 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
            >
              {copy.cta}
              <ArrowRight className="h-4 w-4" />
            </Link>
          </div>

          <ul className="space-y-3 rounded-xl border border-white/10 bg-white/5 p-6">
            {copy.bullets.map((b) => (
              <li key={b} className="flex items-start gap-3 text-sm text-white/85">
                <Check className="mt-0.5 h-4 w-4 shrink-0 text-emerald-400" />
                <span>{b}</span>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </section>
  );
}

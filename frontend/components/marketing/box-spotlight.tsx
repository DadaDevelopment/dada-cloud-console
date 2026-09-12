"use client";

import Link from "next/link";
import { ArrowRight, Box } from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { localeHref } from "@/lib/site";
import { boxCopy } from "@/lib/box-copy";

/** A compact introduction to Box, alongside the existing application-deploy offer. */
export function BoxSpotlight() {
  const { locale } = useLang();
  const product = boxCopy[locale];
  const copy = product.spotlight;

  return (
    <section className="border-y border-blue-100 bg-blue-50/60">
      <div className="mx-auto grid max-w-7xl gap-6 px-4 py-8 sm:px-6 lg:grid-cols-[1fr_auto] lg:items-center lg:px-8 lg:py-10">
        <div className="flex items-start gap-4">
          <span className="mt-1 flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-blue-600 text-white">
            <Box className="h-5 w-5" aria-hidden="true" />
          </span>
          <div className="min-w-0">
            <div className="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs font-semibold">
              <span className="text-blue-700">{copy.eyebrow}</span>
              <span className="text-slate-500">{product.badge}</span>
            </div>
            <h2 className="text-xl font-semibold tracking-tight text-slate-900 sm:text-2xl">{copy.title}</h2>
            <p className="mt-2 max-w-2xl text-sm leading-relaxed text-slate-600">{copy.body}</p>
            <ul className="mt-3 flex flex-wrap gap-x-5 gap-y-1 text-xs text-slate-600">
              {copy.bullets.map((bullet) => (
                <li key={bullet} className="flex items-center gap-2">
                  <span className="h-1 w-1 rounded-full bg-blue-500" aria-hidden="true" />
                  {bullet}
                </li>
              ))}
            </ul>
          </div>
        </div>
        <Link
          href={localeHref("/box", locale)}
          className="inline-flex w-fit items-center gap-3 rounded-lg border border-blue-200 bg-white px-5 py-3 text-sm font-semibold text-blue-700 transition-colors hover:border-blue-400 hover:bg-blue-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-600 focus-visible:ring-offset-2"
        >
          {copy.cta}
          <ArrowRight className="h-4 w-4" aria-hidden="true" />
        </Link>
      </div>
    </section>
  );
}

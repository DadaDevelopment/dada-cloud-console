"use client";
import Link from "next/link";
import type { ReactNode } from "react";

type Tone = "blue" | "violet" | "emerald" | "amber";

const toneRing: Record<Tone, string> = {
  blue: "bg-blue-50 dark:bg-blue-950/40 border-blue-100 dark:border-blue-900 text-blue-600 dark:text-blue-400",
  violet: "bg-violet-50 dark:bg-violet-950/40 border-violet-100 dark:border-violet-900 text-violet-600 dark:text-violet-400",
  emerald:
    "bg-emerald-50 dark:bg-emerald-950/40 border-emerald-100 dark:border-emerald-900 text-emerald-600 dark:text-emerald-400",
  amber: "bg-amber-50 dark:bg-amber-950/40 border-amber-100 dark:border-amber-900 text-amber-600 dark:text-amber-400",
};

export interface ZeroCta {
  label: string;
  onClick?: () => void;
  href?: string;
  disabled?: boolean;
}

/**
 * Onboarding empty state: a centered icon medallion, a clear heading, a short
 * explanation, a primary call-to-action, and an optional numbered "how it works"
 * list. Replaces the flat dashed-box empty states so a first-time user knows what
 * a resource is and what to do next (modeled on the monitoring zero-state).
 */
export function ResourceZeroState({
  icon,
  title,
  description,
  cta,
  steps,
  tone = "blue",
}: {
  icon: ReactNode;
  title: string;
  description: string;
  cta?: ZeroCta;
  steps?: string[];
  tone?: Tone;
}) {
  return (
    <div className="rounded-2xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50/60 dark:bg-gray-900/60 px-6 py-14">
      <div className="mx-auto max-w-lg text-center">
        <div className={`mx-auto mb-5 flex h-16 w-16 items-center justify-center rounded-2xl border ${toneRing[tone]}`}>
          {icon}
        </div>

        <h2 className="text-xl font-semibold text-gray-900 dark:text-gray-100">{title}</h2>
        <p className="mx-auto mt-2 max-w-md text-base leading-relaxed text-gray-600 dark:text-gray-300">{description}</p>

        {cta &&
          (cta.href ? (
            <Link
              href={cta.href}
              className="mt-6 inline-flex items-center gap-2 rounded-lg bg-blue-600 px-5 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 active:bg-blue-800 transition-colors"
            >
              {cta.label}
            </Link>
          ) : (
            <button
              type="button"
              onClick={cta.onClick}
              disabled={cta.disabled}
              className="mt-6 inline-flex items-center gap-2 rounded-lg bg-blue-600 px-5 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 active:bg-blue-800 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {cta.label}
            </button>
          ))}

        {steps && steps.length > 0 && (
          <ol className="mx-auto mt-9 max-w-sm space-y-4 text-left">
            {steps.map((step, i) => (
              <li key={i} className="flex items-start gap-3">
                <span className="flex h-6 w-6 flex-none items-center justify-center rounded-full bg-gray-200 dark:bg-gray-700 text-xs font-bold text-gray-600 dark:text-gray-300">
                  {i + 1}
                </span>
                <p className="pt-0.5 text-sm leading-relaxed text-gray-600 dark:text-gray-300">{step}</p>
              </li>
            ))}
          </ol>
        )}
      </div>
    </div>
  );
}

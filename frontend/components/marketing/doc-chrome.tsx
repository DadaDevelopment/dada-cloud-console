"use client";

import Link from "next/link";
import { ArrowLeft } from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { localeHref } from "@/lib/site";

/**
 * Muted note shown only on the RU docs pages: the guides are English-only for
 * now, so RU readers get a heads-up. English pages render nothing.
 */
export function DocLangNote() {
  const { locale } = useLang();
  if (locale !== "ru") return null;
  return (
    <p className="mb-6 rounded-lg border border-slate-200 bg-slate-50 px-4 py-3 text-sm text-slate-500">
      Документация пока доступна только на английском.
    </p>
  );
}

/**
 * Locale-aware "back to the docs index" link for the top and bottom of a guide.
 */
export function DocBackLink() {
  const { locale } = useLang();
  const label = locale === "ru" ? "Все руководства" : "All guides";
  return (
    <Link
      href={localeHref("/developer", locale)}
      className="inline-flex items-center gap-1.5 text-sm font-medium text-blue-600 hover:text-blue-700"
    >
      <ArrowLeft className="h-4 w-4" />
      {label}
    </Link>
  );
}

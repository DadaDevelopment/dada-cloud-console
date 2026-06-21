"use client";

import { useLang } from "@/lib/i18n/context";
import { clsx } from "clsx";

export function LangToggle({ className }: { className?: string }) {
  const { locale, setLocale } = useLang();
  return (
    <div className={clsx("inline-flex items-center rounded-md border border-white/15 p-0.5 text-xs", className)}>
      {(["ru", "en"] as const).map((l) => (
        <button
          key={l}
          type="button"
          onClick={() => setLocale(l)}
          aria-pressed={locale === l}
          className={clsx(
            "rounded px-2 py-1 font-medium uppercase transition-colors",
            locale === l ? "bg-white text-slate-900" : "text-white/70 hover:text-white",
          )}
        >
          {l}
        </button>
      ))}
    </div>
  );
}

"use client";

import { clsx } from "clsx";
import { CONSOLE_LOCALES } from "@/lib/i18n/console/locale";
import { useT } from "@/lib/i18n/console/context";

/** RU/EN segmented switch for the console top bar. Persists via cookie. */
export function ConsoleLangToggle({ className }: { className?: string }) {
  const { locale, setLocale, t } = useT();
  return (
    <div
      className={clsx(
        "inline-flex items-center rounded-md border border-slate-700 p-0.5 text-xs",
        className,
      )}
      role="group"
      aria-label={t("shell.lang.label")}
    >
      {CONSOLE_LOCALES.map((l) => (
        <button
          key={l}
          type="button"
          onClick={() => setLocale(l)}
          aria-pressed={locale === l}
          className={clsx(
            "rounded px-2 py-1 font-medium uppercase transition-colors",
            locale === l ? "bg-white text-slate-900" : "text-slate-400 hover:text-white",
          )}
        >
          {l}
        </button>
      ))}
    </div>
  );
}

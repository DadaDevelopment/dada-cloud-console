"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useLang } from "@/lib/i18n/context";
import { clsx } from "clsx";

export function LangToggle({ className }: { className?: string }) {
  const { locale } = useLang();
  const pathname = usePathname() || "/";

  // Strip/add the "/en" prefix to build the counterpart URL for the same page.
  const isEn = pathname === "/en" || pathname.startsWith("/en/");
  const ruPath = isEn ? pathname.slice(3) || "/" : pathname;
  const targets: Record<"ru" | "en", string> = {
    ru: ruPath,
    en: ruPath === "/" ? "/en" : `/en${ruPath}`,
  };

  return (
    <div className={clsx("inline-flex items-center rounded-md border border-white/15 p-0.5 text-xs", className)}>
      {(["ru", "en"] as const).map((l) => (
        <Link
          key={l}
          href={targets[l]}
          hrefLang={l}
          aria-current={locale === l ? "true" : undefined}
          className={clsx(
            "rounded px-2 py-1 font-medium uppercase transition-colors",
            locale === l ? "bg-white text-slate-900" : "text-white/70 hover:text-white",
          )}
        >
          {l}
        </Link>
      ))}
    </div>
  );
}

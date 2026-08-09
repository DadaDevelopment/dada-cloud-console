"use client";

import { useState } from "react";
import Link from "next/link";
import { Cloud, Menu, X } from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { useAuth } from "@/lib/auth";
import { consoleHref, localeHref } from "@/lib/site";
import { GOAL_LANDING_CTA, reachGoal } from "@/lib/metrika";
import { LangToggle } from "./lang-toggle";
import { clsx } from "clsx";

export function MarketingHeader() {
  const { t, locale } = useLang();
  const { token } = useAuth();
  const [open, setOpen] = useState(false);

  // Box is the pivot product; it gets a highlighted nav slot in first position.
  // The label is a product name, identical in both locales, so it stays out of Dict.
  const links: { href: string; label: string; highlight?: boolean }[] = [
    { href: localeHref("/box", locale), label: "Box", highlight: true },
    { href: localeHref("/#how", locale), label: t.nav.how },
    { href: localeHref("/cloud-servers", locale), label: t.nav.servers },
    { href: localeHref("/databases", locale), label: t.nav.databases },
    { href: localeHref("/pricing", locale), label: t.nav.pricing },
    { href: localeHref("/developer", locale), label: t.nav.docs },
  ];

  return (
    <header className="sticky top-0 z-50 border-b border-white/10 bg-[#0b1220]/90 backdrop-blur">
      <div className="mx-auto flex h-16 max-w-7xl items-center justify-between px-4 sm:px-6 lg:px-8">
        <div className="flex items-center gap-8">
          <Link href={localeHref("/", locale)} className="flex items-center gap-2 text-white">
            <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-blue-600">
              <Cloud className="h-5 w-5" />
            </span>
            <span className="text-lg font-bold tracking-tight">DADA Cloud</span>
          </Link>
          <nav className="hidden items-center gap-1 md:flex">
            {links.map((l) => (
              <Link
                key={l.href}
                href={l.href}
                className={clsx(
                  "rounded-md px-3 py-2 text-sm font-medium transition-colors",
                  l.highlight
                    ? "text-amber-300 hover:bg-amber-500/10 hover:text-amber-200"
                    : "text-white/75 hover:bg-white/5 hover:text-white",
                )}
              >
                {l.label}
              </Link>
            ))}
          </nav>
        </div>

        <div className="hidden items-center gap-3 md:flex">
          <LangToggle />
          {token ? (
            <Link
              href={consoleHref("/projects")}
              className="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
            >
              {t.nav.console}
            </Link>
          ) : (
            <>
              <Link href={consoleHref("/login")} className="px-3 py-2 text-sm font-medium text-white/80 hover:text-white">
                {t.nav.login}
              </Link>
              <Link
                href={consoleHref(localeHref("/register", locale))}
                onClick={() => reachGoal(GOAL_LANDING_CTA, { source: "direct", placement: "header" })}
                className="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
              >
                {t.nav.register}
              </Link>
            </>
          )}
        </div>

        <button
          type="button"
          className="text-white md:hidden"
          onClick={() => setOpen((v) => !v)}
          aria-label="Menu"
        >
          {open ? <X className="h-6 w-6" /> : <Menu className="h-6 w-6" />}
        </button>
      </div>

      <div className={clsx("border-t border-white/10 bg-[#0b1220] md:hidden", open ? "block" : "hidden")}>
        <nav className="space-y-1 px-4 py-3">
          {links.map((l) => (
            <Link
              key={l.href}
              href={l.href}
              onClick={() => setOpen(false)}
              className={clsx(
                "block rounded-md px-3 py-2 text-sm font-medium",
                l.highlight
                  ? "text-amber-300 hover:bg-amber-500/10 hover:text-amber-200"
                  : "text-white/80 hover:bg-white/5 hover:text-white",
              )}
            >
              {l.label}
            </Link>
          ))}
          <div className="flex items-center justify-between gap-3 pt-3">
            <LangToggle />
            <Link
              href={consoleHref(token ? "/projects" : localeHref("/register", locale))}
              onClick={() => {
                setOpen(false);
                if (!token) {
                  reachGoal(GOAL_LANDING_CTA, { source: "direct", placement: "header_mobile" });
                }
              }}
              className="flex-1 rounded-md bg-blue-600 px-4 py-2 text-center text-sm font-semibold text-white"
            >
              {token ? t.nav.console : t.nav.register}
            </Link>
          </div>
        </nav>
      </div>
    </header>
  );
}

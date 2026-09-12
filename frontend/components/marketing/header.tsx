"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { Cloud, Menu, X } from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { useAuth } from "@/lib/auth";
import { consoleHref, localeHref } from "@/lib/site";
import { GOAL_LANDING_CTA, reachGoal } from "@/lib/metrika";
import { LangToggle } from "./lang-toggle";
import { clsx } from "clsx";
import styles from "./header.module.css";

export function MarketingHeader() {
  const { t, locale } = useLang();
  const { token } = useAuth();
  const [open, setOpen] = useState(false);
  const menuButton = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
        menuButton.current?.focus();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [open]);

  const links: { href: string; label: string; highlight?: boolean }[] = [
    { href: localeHref("/#start", locale), label: locale === "ru" ? "Продукты" : "Products" },
    { href: localeHref("/cloud-servers", locale), label: t.nav.servers },
    { href: localeHref("/box", locale), label: "Dada Box" },
    { href: localeHref("/pricing", locale), label: t.nav.pricing },
    { href: localeHref("/developer", locale), label: t.nav.docs },
  ];

  return (
    <header className={styles.header}>
      <div className={styles.bar}>
        <div className="flex items-center gap-5">
          <Link href={localeHref("/", locale)} className="flex shrink-0 items-center gap-2 whitespace-nowrap text-slate-900" onClick={() => setOpen(false)}>
            <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-blue-600">
              <Cloud className="h-5 w-5 text-white" />
            </span>
            <span className="text-lg font-semibold tracking-tight">DADA Cloud</span>
          </Link>
          <nav aria-label={locale === "ru" ? "Основная навигация" : "Main navigation"} className="hidden items-center gap-1 xl:flex">
            {links.map((l) => (
              <Link
                key={l.href}
                href={l.href}
                className={clsx(
                  "whitespace-nowrap rounded-md px-2.5 py-2 text-sm font-medium transition-colors",
                  l.highlight
                    ? "text-amber-300 hover:bg-amber-500/10 hover:text-amber-200"
                    : "text-slate-600 hover:bg-slate-100 hover:text-blue-600",
                )}
              >
                {l.label}
              </Link>
            ))}
          </nav>
        </div>

        <div className="hidden items-center gap-3 xl:flex">
          <LangToggle className={styles.language} />
          {token ? (
            <Link
              href={consoleHref("/projects")}
              className="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
            >
              {t.nav.console}
            </Link>
          ) : (
            <>
              <Link href={consoleHref("/login")} className="px-3 py-2 text-sm font-medium text-slate-600 hover:text-blue-600">
                {t.nav.login}
              </Link>
              <Link
                href={consoleHref("/login")}
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
          ref={menuButton}
          className="flex h-11 w-11 items-center justify-center rounded-md text-slate-900 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-400 xl:hidden"
          onClick={() => setOpen((v) => !v)}
          aria-label={locale === "ru" ? (open ? "Закрыть меню" : "Открыть меню") : (open ? "Close menu" : "Open menu")}
          aria-expanded={open}
          aria-controls="marketing-mobile-menu"
        >
          {open ? <X className="h-6 w-6" /> : <Menu className="h-6 w-6" />}
        </button>
      </div>

      <div id="marketing-mobile-menu" className={clsx("max-h-[calc(100dvh-4.5rem)] overflow-y-auto border-t border-slate-200 bg-white xl:hidden", open ? "block" : "hidden")}>
        <nav
          aria-label={locale === "ru" ? "Мобильная навигация" : "Mobile navigation"}
          className="space-y-1 px-4 py-3"
          onClick={(event) => {
            if ((event.target as HTMLElement).closest("a")) setOpen(false);
          }}
        >
          {links.map((l) => (
            <Link
              key={l.href}
              href={l.href}
              onClick={() => setOpen(false)}
              className={clsx(
                "block rounded-md px-3 py-2 text-sm font-medium",
                l.highlight
                  ? "text-amber-300 hover:bg-amber-500/10 hover:text-amber-200"
                  : "text-slate-600 hover:bg-slate-100 hover:text-blue-600",
              )}
            >
              {l.label}
            </Link>
          ))}
          <div className="flex items-center justify-between gap-3 pt-3">
            <LangToggle className={styles.language} />
            <Link
              href={consoleHref(token ? "/projects" : "/login")}
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

"use client";
import { useEffect, useState } from "react";
import { ProjectSwitcher } from "./project-switcher";
import { EnvSelector } from "./env-selector";
import { OrgSwitcher } from "./org-switcher";
import { AccountMenu } from "./account-menu";
import { ConsoleLangToggle } from "./console-lang-toggle";
import { ThemeToggle } from "./theme-toggle";
import { useT } from "@/lib/i18n/console/context";

/** Detects platform for the ⌘ / Ctrl hint without breaking SSR. */
function useMetaKeyLabel() {
  const [label, setLabel] = useState("Ctrl");
  useEffect(() => {
    if (typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform)) {
      // One-shot platform detection (can't read navigator during SSR).
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setLabel("⌘");
    }
  }, []);
  return label;
}

function SearchIcon({ className }: { className?: string }) {
  return (
    <svg className={className} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5} aria-hidden="true">
      <path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z" />
    </svg>
  );
}

/**
 * Global top bar: brand, persistent project switcher, command-palette trigger,
 * environment selector and account menu. Project context now lives in the
 * chrome (the YC/AWS/GCP/VK pattern) instead of a flat list you return to.
 * On mobile the sidebar collapses into a drawer toggled by the hamburger here.
 */
export function TopBar({
  onOpenPalette,
  onToggleNav,
  navOpen = false,
}: {
  onOpenPalette: () => void;
  /** Present only when a project is selected (i.e. the sidebar exists). */
  onToggleNav?: () => void;
  navOpen?: boolean;
}) {
  const metaKey = useMetaKeyLabel();
  const { t } = useT();
  return (
    <header className="flex h-14 shrink-0 items-center gap-2 border-b border-slate-700 bg-slate-900 px-3 sm:h-16 sm:gap-3 sm:px-4">
      {onToggleNav && (
        <button
          type="button"
          onClick={onToggleNav}
          aria-expanded={navOpen}
          aria-label={navOpen ? t("shell.nav.closeMenu") : t("shell.nav.openMenu")}
          className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border border-slate-700 text-slate-300 transition-colors hover:bg-slate-800 hover:text-white lg:hidden"
        >
          {navOpen ? (
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5} aria-hidden="true">
              <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          ) : (
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5} aria-hidden="true">
              <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5" />
            </svg>
          )}
        </button>
      )}

      <div className="hidden items-center gap-2 pr-1 sm:flex">
        <div className="flex h-7 w-7 items-center justify-center rounded-md bg-blue-600">
          <svg className="h-4 w-4 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor" aria-hidden="true">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 15a4 4 0 004 4h9a5 5 0 10-.1-9.999 5.002 5.002 0 10-9.78 2.096A4.001 4.001 0 003 15z" />
          </svg>
        </div>
        <span className="hidden text-sm font-semibold text-white lg:inline">DADA Console</span>
      </div>

      <div className="hidden items-center gap-2 md:flex md:gap-3">
        <OrgSwitcher />
        <span className="text-slate-600">/</span>
      </div>
      <ProjectSwitcher />
      <EnvSelector />

      <button
        type="button"
        onClick={onOpenPalette}
        className="ml-2 hidden h-9 items-center gap-2 rounded-lg border border-slate-700 bg-slate-800 px-3 text-sm text-slate-400 transition-colors hover:bg-slate-700 md:flex"
        aria-label={t("shell.openSearch")}
      >
        <SearchIcon className="h-4 w-4" />
        <span>{t("shell.search")}</span>
        <kbd className="rounded border border-slate-600 px-1 text-xs text-slate-400">{metaKey} K</kbd>
      </button>

      <div className="ml-auto flex items-center gap-1.5 sm:gap-3">
        <button
          type="button"
          onClick={onOpenPalette}
          className="flex h-9 w-9 items-center justify-center rounded-lg border border-slate-700 text-slate-400 transition-colors hover:bg-slate-800 hover:text-white md:hidden"
          aria-label={t("shell.openSearch")}
        >
          <SearchIcon className="h-4 w-4" />
        </button>
        <ThemeToggle />
        <ConsoleLangToggle />
        <AccountMenu />
      </div>
    </header>
  );
}

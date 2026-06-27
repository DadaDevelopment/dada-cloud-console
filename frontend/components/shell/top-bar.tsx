"use client";
import { useEffect, useState } from "react";
import { ProjectSwitcher } from "./project-switcher";
import { OrgSwitcher } from "./org-switcher";
import { AccountMenu } from "./account-menu";
import { ConsoleLangToggle } from "./console-lang-toggle";
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

/**
 * Global top bar: brand, persistent project switcher, command-palette trigger,
 * environment selector and account menu. Project context now lives in the
 * chrome (the YC/AWS/GCP/VK pattern) instead of a flat list you return to.
 */
export function TopBar({ onOpenPalette }: { onOpenPalette: () => void }) {
  const metaKey = useMetaKeyLabel();
  const { t } = useT();
  return (
    <header className="flex h-16 shrink-0 items-center gap-3 border-b border-slate-700 bg-slate-900 px-4">
      <div className="flex items-center gap-2 pr-1">
        <div className="flex h-7 w-7 items-center justify-center rounded-md bg-blue-600">
          <svg className="h-4 w-4 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor" aria-hidden="true">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 15a4 4 0 004 4h9a5 5 0 10-.1-9.999 5.002 5.002 0 10-9.78 2.096A4.001 4.001 0 003 15z" />
          </svg>
        </div>
        <span className="hidden text-sm font-semibold text-white sm:inline">DADA Console</span>
      </div>

      <OrgSwitcher />
      <span className="text-slate-600">/</span>
      <ProjectSwitcher />

      <button
        type="button"
        onClick={onOpenPalette}
        className="ml-2 hidden h-9 items-center gap-2 rounded-lg border border-slate-700 bg-slate-800 px-3 text-sm text-slate-400 transition-colors hover:bg-slate-700 md:flex"
        aria-label={t("shell.openSearch")}
      >
        <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5} aria-hidden="true">
          <path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z" />
        </svg>
        <span>{t("shell.search")}</span>
        <kbd className="rounded border border-slate-600 px-1 text-xs text-slate-400">{metaKey} K</kbd>
      </button>

      <div className="ml-auto flex items-center gap-3">
        <ConsoleLangToggle />
        <AccountMenu />
      </div>
    </header>
  );
}

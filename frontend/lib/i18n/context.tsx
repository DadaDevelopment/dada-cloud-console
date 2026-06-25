"use client";

import { createContext, useContext, useEffect } from "react";
import { usePathname } from "next/navigation";
import { dictionaries, DEFAULT_LOCALE, type Locale, type Dict } from "./dict";

interface LangContextValue {
  locale: Locale;
  t: Dict;
}

const LangContext = createContext<LangContextValue | null>(null);

// The URL is the single source of truth for the marketing locale: "/en" (and
// "/en/...") renders English, everything else renders the default (RU). This
// makes each language a real, crawlable URL that also renders correctly on the
// server, instead of a client-only localStorage toggle.
export function localeFromPath(pathname: string | null | undefined): Locale {
  if (pathname === "/en" || (pathname?.startsWith("/en/") ?? false)) return "en";
  return DEFAULT_LOCALE;
}

export function LangProvider({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const locale = localeFromPath(pathname);

  useEffect(() => {
    // Root layout renders <html lang="en"> (shared with the console). Correct it
    // to the locale this URL actually renders so crawlers and assistive tech agree.
    if (typeof document !== "undefined") {
      document.documentElement.lang = locale;
    }
  }, [locale]);

  return (
    <LangContext.Provider value={{ locale, t: dictionaries[locale] }}>
      {children}
    </LangContext.Provider>
  );
}

export function useLang(): LangContextValue {
  const ctx = useContext(LangContext);
  if (!ctx) throw new Error("useLang must be used within LangProvider");
  return ctx;
}

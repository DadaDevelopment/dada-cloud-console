"use client";

import { createContext, useContext, useEffect, useState, useCallback } from "react";
import { dictionaries, DEFAULT_LOCALE, type Locale, type Dict } from "./dict";

const STORAGE_KEY = "dada_lang";

interface LangContextValue {
  locale: Locale;
  setLocale: (l: Locale) => void;
  toggle: () => void;
  t: Dict;
}

const LangContext = createContext<LangContextValue | null>(null);

export function LangProvider({ children }: { children: React.ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(DEFAULT_LOCALE);

  useEffect(() => {
    const stored = (typeof window !== "undefined" && localStorage.getItem(STORAGE_KEY)) as Locale | null;
    const active = stored === "ru" || stored === "en" ? stored : DEFAULT_LOCALE;
    if (stored === "ru" || stored === "en") {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setLocaleState(stored);
    }
    // Root layout renders <html lang="en"> (shared with the console). On the
    // RU-default marketing host, correct the document language so crawlers and
    // assistive tech see the locale actually being rendered.
    if (typeof document !== "undefined") {
      document.documentElement.lang = active;
    }
  }, []);

  const setLocale = useCallback((l: Locale) => {
    setLocaleState(l);
    if (typeof window !== "undefined") {
      localStorage.setItem(STORAGE_KEY, l);
      document.documentElement.lang = l;
    }
  }, []);

  const toggle = useCallback(() => {
    setLocale(locale === "ru" ? "en" : "ru");
  }, [locale, setLocale]);

  return (
    <LangContext.Provider value={{ locale, setLocale, toggle, t: dictionaries[locale] }}>
      {children}
    </LangContext.Provider>
  );
}

export function useLang(): LangContextValue {
  const ctx = useContext(LangContext);
  if (!ctx) throw new Error("useLang must be used within LangProvider");
  return ctx;
}

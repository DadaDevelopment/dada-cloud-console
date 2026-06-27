"use client";

import { createContext, useCallback, useContext, useEffect, useState } from "react";
import {
  DEFAULT_CONSOLE_LOCALE,
  readConsoleLocaleCookie,
  writeConsoleLocaleCookie,
  type ConsoleLocale,
} from "./locale";
import { messages } from "./messages";

interface ConsoleLangValue {
  locale: ConsoleLocale;
  setLocale: (locale: ConsoleLocale) => void;
  /** Translate a key, interpolating {placeholders} from `vars`. */
  t: (key: string, vars?: Record<string, string | number>) => string;
}

const ConsoleLangContext = createContext<ConsoleLangValue | null>(null);

function interpolate(template: string, vars?: Record<string, string | number>): string {
  if (!vars) return template;
  return template.replace(/\{(\w+)\}/g, (_, name) =>
    name in vars ? String(vars[name]) : `{${name}}`,
  );
}

/**
 * Console language provider. SSR and the first client render both use the
 * default locale (RU) so hydration matches; a mount effect then adopts the
 * cookie preference. Default-locale users — the majority — see no flash.
 */
export function ConsoleLangProvider({ children }: { children: React.ReactNode }) {
  const [locale, setLocaleState] = useState<ConsoleLocale>(DEFAULT_CONSOLE_LOCALE);

  useEffect(() => {
    const stored = readConsoleLocaleCookie();
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (stored && stored !== locale) setLocaleState(stored);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (typeof document !== "undefined") document.documentElement.lang = locale;
  }, [locale]);

  const setLocale = useCallback((next: ConsoleLocale) => {
    writeConsoleLocaleCookie(next);
    setLocaleState(next);
  }, []);

  const t = useCallback(
    (key: string, vars?: Record<string, string | number>) => {
      const entry = messages[key];
      if (!entry) return key;
      return interpolate(entry[locale], vars);
    },
    [locale],
  );

  return (
    <ConsoleLangContext.Provider value={{ locale, setLocale, t }}>
      {children}
    </ConsoleLangContext.Provider>
  );
}

export function useT(): ConsoleLangValue {
  const ctx = useContext(ConsoleLangContext);
  if (!ctx) throw new Error("useT must be used within ConsoleLangProvider");
  return ctx;
}

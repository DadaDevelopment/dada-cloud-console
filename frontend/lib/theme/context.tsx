"use client";
import { createContext, useCallback, useContext, useEffect, useState } from "react";
import {
  applyThemeClass,
  resolveInitialTheme,
  writeThemeCookie,
  type Theme,
} from "./theme";

interface ThemeContextValue {
  theme: Theme;
  setTheme: (t: Theme) => void;
  toggle: () => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

/**
 * ThemeProvider owns the console color theme. It resolves the initial theme
 * (cookie → OS) on mount, applies the `.dark` class to <html>, and persists any
 * change. SSR renders the light default; the mount effect reconciles to the real
 * preference (the console is auth-gated behind a spinner, so any flash is hidden).
 */
export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setThemeState] = useState<Theme>("light");

  useEffect(() => {
    const initial = resolveInitialTheme();
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setThemeState(initial);
    applyThemeClass(initial);
  }, []);

  const setTheme = useCallback((t: Theme) => {
    setThemeState(t);
    applyThemeClass(t);
    writeThemeCookie(t);
  }, []);

  const toggle = useCallback(() => {
    setThemeState((prev) => {
      const next: Theme = prev === "dark" ? "light" : "dark";
      applyThemeClass(next);
      writeThemeCookie(next);
      return next;
    });
  }, []);

  return <ThemeContext.Provider value={{ theme, setTheme, toggle }}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error("useTheme must be used within ThemeProvider");
  return ctx;
}

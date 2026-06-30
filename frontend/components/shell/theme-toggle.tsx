"use client";
import { Moon, Sun } from "lucide-react";
import { clsx } from "clsx";
import { useTheme } from "@/lib/theme/context";

/** Light/dark switch for the console top bar. Persists via cookie. */
export function ThemeToggle({ className }: { className?: string }) {
  const { theme, toggle } = useTheme();
  const isDark = theme === "dark";
  return (
    <button
      type="button"
      onClick={toggle}
      aria-pressed={isDark}
      title={isDark ? "Switch to light theme" : "Switch to dark theme"}
      className={clsx(
        "flex h-9 w-9 items-center justify-center rounded-lg border border-slate-700 text-slate-400 transition-colors hover:bg-slate-800 hover:text-white",
        className,
      )}
    >
      {isDark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
    </button>
  );
}

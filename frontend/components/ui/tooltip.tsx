"use client";
import { useState, useId } from "react";

/**
 * Lightweight hover/focus tooltip for truncated values (URIs, digests, names).
 * Keyboard-accessible (shows on focus) and announced via aria-describedby.
 * For purely decorative hints prefer a native `title`; use this when the full
 * value matters and may be truncated.
 */
export function Tooltip({
  label,
  children,
  className,
}: {
  label: string;
  children: React.ReactNode;
  className?: string;
}) {
  const [open, setOpen] = useState(false);
  const id = useId();

  return (
    <span
      className={`relative inline-flex ${className ?? ""}`}
      onMouseEnter={() => setOpen(true)}
      onMouseLeave={() => setOpen(false)}
      onFocus={() => setOpen(true)}
      onBlur={() => setOpen(false)}
      tabIndex={0}
      aria-describedby={open ? id : undefined}
    >
      {children}
      {open && (
        <span
          id={id}
          role="tooltip"
          className="pointer-events-none absolute bottom-full left-1/2 z-50 mb-1.5 max-w-xs -translate-x-1/2 whitespace-normal break-all rounded-md bg-slate-900 dark:bg-gray-700 px-2 py-1 text-xs font-normal text-white shadow-lg"
        >
          {label}
        </span>
      )}
    </span>
  );
}

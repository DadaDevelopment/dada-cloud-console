"use client";
import { useState, useCallback } from "react";
import { clsx } from "clsx";

/**
 * Copy-to-clipboard button. Used for API keys, URIs and other values that were
 * previously shown in bare `<pre>` blocks with no affordance to copy.
 * Shows a transient check state on success.
 */
export function CopyButton({
  value,
  label = "Copy",
  className,
}: {
  value: string;
  label?: string;
  className?: string;
}) {
  const [copied, setCopied] = useState(false);

  const copy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard API unavailable (e.g. insecure context) — no-op.
    }
  }, [value]);

  return (
    <button
      type="button"
      onClick={copy}
      aria-label={copied ? "Copied" : label}
      className={clsx(
        "inline-flex items-center gap-1.5 rounded-md border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-900 px-2 py-1 text-xs font-medium text-gray-600 dark:text-gray-400 transition-colors hover:bg-gray-50 dark:hover:bg-gray-800 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500",
        className,
      )}
    >
      {copied ? (
        <>
          <svg className="h-3.5 w-3.5 text-green-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2} aria-hidden="true">
            <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
          </svg>
          Copied
        </>
      ) : (
        <>
          <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5} aria-hidden="true">
            <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 17.25v3.375c0 .621-.504 1.125-1.125 1.125h-9.75a1.125 1.125 0 01-1.125-1.125V7.875c0-.621.504-1.125 1.125-1.125H6.75a9.06 9.06 0 011.5.124m7.5 10.376h3.375c.621 0 1.125-.504 1.125-1.125V11.25c0-4.46-3.243-8.161-7.5-8.876a9.06 9.06 0 00-1.5-.124H9.375c-.621 0-1.125.504-1.125 1.125v3.5m7.5 10.375H9.375a1.125 1.125 0 01-1.125-1.125v-9.25m12 6.625v-1.875a3.375 3.375 0 00-3.375-3.375h-1.5a1.125 1.125 0 01-1.125-1.125v-1.5a3.375 3.375 0 00-3.375-3.375H9.75" />
          </svg>
          {label}
        </>
      )}
    </button>
  );
}

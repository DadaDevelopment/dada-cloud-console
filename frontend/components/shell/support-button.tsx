"use client";

import { usePathname } from "next/navigation";
import { useT } from "@/lib/i18n/console/context";

const SUPPORT_EMAIL = "development@dada-tuda.ru";

function SupportIcon() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.5} className="h-5 w-5" aria-hidden="true">
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        d="M8.625 12a.375.375 0 11-.75 0 .375.375 0 01.75 0zm3.75 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm3.75 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zM21 12c0 4.556-4.03 8.25-9 8.25a9.764 9.764 0 01-2.555-.337A5.972 5.972 0 015.41 20.97a5.969 5.969 0 01-.474-.065 4.48 4.48 0 00.978-2.025c.09-.457-.133-.901-.467-1.226C3.93 16.178 3 14.189 3 12c0-4.556 4.03-8.25 9-8.25s9 3.694 9 8.25z"
      />
    </svg>
  );
}

/**
 * Unobtrusive floating support link in the authed console shell. Opens the
 * user's mail client addressed to the support inbox; does not replace or
 * change any existing flow.
 */
export function SupportButton() {
  const { t } = useT();
  const pathname = usePathname();
  const body = `Route: ${pathname ?? "unknown"}`;
  const mailto = `mailto:${SUPPORT_EMAIL}?subject=${encodeURIComponent(
    t("feedback.support.mailSubject"),
  )}&body=${encodeURIComponent(body)}`;

  return (
    <a
      href={mailto}
      title={t("feedback.support.label")}
      aria-label={t("feedback.support.label")}
      className="fixed bottom-4 left-4 z-40 flex h-11 w-11 items-center justify-center rounded-full border border-gray-200 bg-white text-gray-600 shadow-lg transition-colors hover:bg-gray-50 hover:text-gray-900 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-300 dark:hover:bg-gray-800 dark:hover:text-white"
    >
      <SupportIcon />
    </a>
  );
}

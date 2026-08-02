"use client";
import Link from "next/link";
import { useT } from "@/lib/i18n/console/context";

/**
 * Tab strip shared by the Databases and Object Storage pages. The sidebar
 * carries one "Data" entry for both, so the switch between them lives on the
 * page itself. Links rather than a Radix Tabs root: each tab is a real route
 * with its own data fetching, and a client-side tab would strand deep links.
 */
export function DataTabs({ projectId, active }: { projectId: string; active: "databases" | "storage" }) {
  const { t } = useT();
  const tabs: Array<{ key: "databases" | "storage"; href: string }> = [
    { key: "databases", href: `/projects/${projectId}/databases` },
    { key: "storage", href: `/projects/${projectId}/storage` },
  ];
  return (
    <div className="mt-3 inline-flex items-center gap-1 rounded-lg border border-gray-200 bg-gray-50 p-1 dark:border-gray-800 dark:bg-gray-900">
      {tabs.map((tab) => (
        <Link
          key={tab.key}
          href={tab.href}
          data-ux={`data_tab:${tab.key}`}
          aria-current={tab.key === active ? "page" : undefined}
          className={`inline-flex items-center justify-center rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
            tab.key === active
              ? "bg-white text-gray-900 shadow-sm dark:bg-gray-800 dark:text-gray-100"
              : "text-gray-500 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200"
          }`}
        >
          {t(`nav.${tab.key}`)}
        </Link>
      ))}
    </div>
  );
}

"use client";
import Link from "next/link";
import { useT } from "@/lib/i18n/console/context";

/**
 * Cross-links the platform-admin pages (Overview / Audit / Approvals) so a
 * platform-admin can hop between them without going back through the account
 * menu each time.
 */
export type AdminTab = "overview" | "costs" | "db-shards" | "ai-gateway" | "audit" | "feedback" | "approvals";

const TABS: { id: AdminTab; href: string; labelKey: string }[] = [
  { id: "overview", href: "/admin", labelKey: "adminOverview.crumb.overview" },
  { id: "costs", href: "/admin/costs", labelKey: "adminCosts.crumb.costs" },
  { id: "db-shards", href: "/admin/db-shards", labelKey: "adminDbShards.crumb.dbShards" },
  { id: "ai-gateway", href: "/admin/ai-gateway", labelKey: "aiGateway.crumb.aiGateway" },
  { id: "audit", href: "/admin/audit", labelKey: "audit.crumb.audit" },
  { id: "feedback", href: "/admin/feedback", labelKey: "adminFeedback.crumb.feedback" },
  { id: "approvals", href: "/admin/approvals", labelKey: "approvals.crumb.approvals" },
];

export function AdminTabs({ active }: { active: AdminTab }) {
  const { t } = useT();
  return (
    <nav className="mb-6 flex gap-1 border-b border-gray-200 dark:border-gray-800">
      {TABS.map((tab) => (
        <Link
          key={tab.id}
          href={tab.href}
          className={`border-b-2 px-3 py-2 text-sm font-medium transition-colors ${
            tab.id === active
              ? "border-blue-600 text-blue-600 dark:border-blue-400 dark:text-blue-400"
              : "border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-700 dark:text-gray-400 dark:hover:border-gray-700 dark:hover:text-gray-200"
          }`}
        >
          {t(tab.labelKey)}
        </Link>
      ))}
    </nav>
  );
}

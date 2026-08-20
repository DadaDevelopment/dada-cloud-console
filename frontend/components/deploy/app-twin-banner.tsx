"use client";

import { Copy } from "lucide-react";
import Link from "next/link";
import { useT } from "@/lib/i18n/console/context";
import { resolveAppTwin } from "@/lib/app-twin";

interface AppTwinBannerProps {
  twinOf: unknown;
  onDeleteThis: () => void;
}

/**
 * Informational (amber, not red) notice that this app was built from the
 * same git repo as another app in a different project -- see
 * `lib/app-twin.ts` for how the backend's `twin_of` field on summary_json
 * is turned into a descriptor. This is not an outage: the app itself may be
 * healthy, it is simply a duplicate, so it must never render with the same
 * urgency as a crash banner.
 *
 * The delete action reuses the page's own danger-zone delete flow
 * (`setDeleteTarget` -> `DeleteImpactModal`) via `onDeleteThis` rather than
 * calling any API directly, so the confirmation, impact preview, and audit
 * trail stay exactly what deleting from the danger zone already produces.
 */
export function AppTwinBanner({ twinOf, onDeleteThis }: AppTwinBannerProps) {
  const { t } = useT();
  const twin = resolveAppTwin(twinOf);
  if (!twin) return null;

  return (
    <div className="mb-6 rounded-xl border border-amber-300 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/30 p-5 shadow-sm">
      <p className="flex items-center gap-1.5 text-sm font-semibold text-amber-800 dark:text-amber-300">
        <Copy className="h-4 w-4 shrink-0" />
        {t("apps.twin.title")}
      </p>
      <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">
        {t("apps.twin.desc", {
          repo: twin.repoFullName,
          appName: twin.appName,
          projectName: twin.projectName,
        })}
      </p>
      <div className="mt-3 flex flex-wrap gap-2">
        <Link
          href={twin.href}
          data-ux="app_twin_banner:open_twin"
          className="inline-flex items-center gap-1.5 rounded-lg bg-amber-600 px-3 py-1.5 text-xs font-semibold text-white shadow-sm hover:bg-amber-700 transition-colors"
        >
          {t("apps.twin.openTwin")}
        </Link>
        <button
          type="button"
          onClick={onDeleteThis}
          data-ux="app_twin_banner:delete_this"
          className="inline-flex items-center gap-1.5 rounded-lg border border-amber-300 dark:border-amber-800 bg-white dark:bg-gray-900 px-3 py-1.5 text-xs font-medium text-amber-800 dark:text-amber-300 hover:bg-amber-100 dark:hover:bg-amber-950/40 transition-colors"
        >
          {t("apps.twin.deleteThis")}
        </button>
      </div>
    </div>
  );
}

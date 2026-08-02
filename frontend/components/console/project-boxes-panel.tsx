"use client";
import { useEffect, useState } from "react";
import { boxesApi } from "@/lib/api";
import type { Box } from "@/lib/types";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { timeAgo } from "@/lib/format";
import { useT } from "@/lib/i18n/console/context";

/**
 * Read-only list of the project's boxes on the overview.
 *
 * Boxes are created and driven by agents over MCP, not by hand: telemetry over
 * 07-31..08-02 recorded two clicks on the whole Boxes surface. The console's
 * job is therefore to answer "is one running, and what is it costing me",
 * which is a panel, not a management screen. Renders nothing when the project
 * has no boxes, so the overview stays short for everyone else.
 */
export function ProjectBoxesPanel({ projectId }: { projectId: string }) {
  const { t } = useT();
  const [boxes, setBoxes] = useState<Box[]>([]);

  useEffect(() => {
    let cancelled = false;
    boxesApi
      .list(projectId)
      .then((r) => {
        if (!cancelled) setBoxes(r.boxes ?? []);
      })
      .catch(() => {
        if (!cancelled) setBoxes([]);
      });
    return () => {
      cancelled = true;
    };
  }, [projectId]);

  if (boxes.length === 0) return null;

  return (
    <div className="mb-8">
      <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("nav.boxes")}</h2>
      <div className="overflow-hidden rounded-xl border border-gray-200 dark:border-gray-800">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 text-left text-xs uppercase tracking-wide text-gray-500 dark:bg-gray-900 dark:text-gray-400">
            <tr>
              <th className="px-4 py-2 font-medium">{t("overview.boxes.col.name")}</th>
              <th className="px-4 py-2 font-medium">{t("overview.boxes.col.status")}</th>
              <th className="px-4 py-2 font-medium">{t("overview.boxes.col.profile")}</th>
              <th className="px-4 py-2 font-medium">{t("overview.boxes.col.active")}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
            {boxes.map((box) => (
              <tr key={box.id} className="bg-white dark:bg-gray-950">
                <td className="px-4 py-2 font-medium text-gray-900 dark:text-gray-100">{box.name}</td>
                <td className="px-4 py-2">
                  <PhaseBadge phase={box.status} />
                </td>
                <td className="px-4 py-2 text-gray-500 dark:text-gray-400">{box.profile}</td>
                <td className="px-4 py-2 text-gray-500 dark:text-gray-400">
                  {box.last_active_at ? timeAgo(box.last_active_at) : timeAgo(box.created_at)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="mt-2 text-xs text-gray-500 dark:text-gray-400">{t("overview.boxes.readonly")}</p>
    </div>
  );
}

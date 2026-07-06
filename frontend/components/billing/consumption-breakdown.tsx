"use client";

import { ResourceIcon } from "@/components/shell/icons";
import { EmptyState } from "@/components/ui/empty-state";
import type { IconName } from "@/lib/resources";
import type { ConsumptionResource, ConsumptionResponse } from "@/lib/api";
import { formatRub } from "@/lib/format";
import { useT } from "@/lib/i18n/console/context";

type Kind = ConsumptionResource["kind"];

const GROUP_ORDER: Kind[] = ["app", "database", "storage"];

const KIND_ICON: Record<Kind, IconName> = {
  app: "apps",
  database: "databases",
  storage: "storage",
};

/**
 * Beget-style grouped consumption table. Groups resources by kind, prints a
 * per-group subtotal and a bold grand total, all rendered as a money-equivalent
 * estimate ("оценка по текущим тарифам, не счёт"). Reused by both the project
 * dashboard card and the full billing page so the breakdown stays identical.
 */
export function ConsumptionBreakdown({ data }: { data: ConsumptionResponse }) {
  const { t } = useT();

  if (data.resources.length === 0) {
    return (
      <EmptyState
        title={t("consumption.empty.title")}
        description={t("consumption.empty.description")}
      />
    );
  }

  const perMonth = (amount: number) => t("consumption.perMonth", { amount: formatRub(amount) });

  const groups = GROUP_ORDER.map((kind) => ({
    kind,
    rows: data.resources.filter((r) => r.kind === kind),
  })).filter((g) => g.rows.length > 0);

  return (
    <div className="overflow-hidden rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900">
      {groups.map((group, gi) => {
        const subtotal = group.rows.reduce((sum, r) => sum + r.cost_rub, 0);
        return (
          <div key={group.kind} className={gi > 0 ? "border-t border-gray-100 dark:border-gray-800" : ""}>
            <div className="flex items-center gap-2 bg-gray-50 dark:bg-gray-950/40 px-5 py-2.5">
              <ResourceIcon name={KIND_ICON[group.kind]} className="h-4 w-4 text-gray-400 dark:text-gray-500" />
              <span className="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
                {t(`consumption.group.${group.kind}`)}
              </span>
            </div>
            {group.rows.map((r) => (
              <div
                key={`${r.kind}:${r.name}`}
                className="flex items-center gap-4 border-t border-gray-100 dark:border-gray-800 px-5 py-3"
              >
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium text-gray-900 dark:text-gray-100">{r.name}</p>
                  <p className="mt-0.5 truncate text-xs text-gray-400 dark:text-gray-500">{usageSubtext(r, t)}</p>
                </div>
                <span className="shrink-0 text-sm tabular-nums text-gray-700 dark:text-gray-200">
                  {perMonth(r.cost_rub)}
                </span>
              </div>
            ))}
            <div className="flex items-center justify-between border-t border-gray-100 dark:border-gray-800 px-5 py-2.5">
              <span className="text-xs text-gray-400 dark:text-gray-500">{t("consumption.subtotal")}</span>
              <span className="text-xs font-medium tabular-nums text-gray-500 dark:text-gray-400">{perMonth(subtotal)}</span>
            </div>
          </div>
        );
      })}
      <div className="flex items-center justify-between border-t-2 border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-950/40 px-5 py-3.5">
        <span className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("consumption.total")}</span>
        <span className="text-sm font-bold tabular-nums text-gray-900 dark:text-gray-100">{perMonth(data.total_rub)}</span>
      </div>
    </div>
  );
}

function usageSubtext(r: ConsumptionResource, t: (key: string, vars?: Record<string, string | number>) => string): string {
  if (r.kind === "app") {
    return t("consumption.usage.compute", {
      cpu: (r.cpu_cores ?? 0).toFixed(2),
      ram: (r.ram_gb ?? 0).toFixed(1),
    });
  }
  return t("consumption.usage.storage", { gb: (r.storage_gb ?? 0).toFixed(1) });
}

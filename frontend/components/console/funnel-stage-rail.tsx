export interface FunnelStageRailItem {
  key: string;
  label: string;
  count: number;
  detail?: string;
  rateFromPrevious?: number;
}

/**
 * A compact, count-first funnel for stages whose unit is explicit in their
 * labels. Rates are supplied only for transitions that share a real cohort.
 */
export function FunnelStageRail({ items, ariaLabel }: { items: FunnelStageRailItem[]; ariaLabel: string }) {
  const largest = Math.max(...items.map((item) => item.count), 1);

  return (
    <ol className="grid gap-2 md:grid-cols-2 xl:grid-cols-4" aria-label={ariaLabel}>
      {items.map((item) => (
        <li key={item.key} className="rounded-lg border border-gray-200 bg-gray-50 p-3 dark:border-gray-700 dark:bg-gray-800/50">
          <div className="flex items-start justify-between gap-2">
            <span className="text-xs font-medium text-gray-600 dark:text-gray-300">{item.label}</span>
            {item.rateFromPrevious !== undefined && (
              <span className="text-xs tabular-nums text-gray-400 dark:text-gray-500">{item.rateFromPrevious.toFixed(1)}%</span>
            )}
          </div>
          <strong className="mt-2 block text-2xl font-semibold tabular-nums text-gray-900 dark:text-gray-100">{item.count}</strong>
          <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700">
            <div className="h-full rounded-full bg-indigo-500" style={{ width: `${Math.max((item.count / largest) * 100, item.count > 0 ? 3 : 0)}%` }} />
          </div>
          {item.detail && <p className="mt-2 text-xs leading-4 text-gray-500 dark:text-gray-400">{item.detail}</p>}
        </li>
      ))}
    </ol>
  );
}

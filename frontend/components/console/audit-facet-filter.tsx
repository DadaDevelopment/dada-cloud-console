"use client";
import { useMemo, useState } from "react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { useT } from "@/lib/i18n/console/context";

export interface FacetOption {
  value: string;
  count: number;
  display?: string;
  badge?: string;
  badgeClass?: string;
}

/**
 * Below this many options the search box costs more than it saves: the whole
 * list already fits without scrolling, so the input is one more thing to skip
 * past. The cohort facet has four values and lands under it.
 */
const SEARCHABLE_FROM = 8;

interface AuditFacetFilterProps {
  label: string;
  options: FacetOption[];
  hidden: Set<string>;
  onChange: (hidden: Set<string>) => void;
}

/**
 * A checkbox popover over one audit facet (actors or actions).
 *
 * The list is what to SHOW: every box starts ticked and unticking one hides
 * that value. Hiding rather than picking is what the trail needs — two chatty
 * actions carried 46% of a week's rows, and the operator wants those two gone
 * without having to re-tick every action instrumented since.
 *
 * The counts sit next to each row because they are the whole reason to untick:
 * a value with 3 rows is not the one drowning the page.
 */
export function AuditFacetFilter({ label, options, hidden, onChange }: AuditFacetFilterProps) {
  const { t } = useT();
  const [query, setQuery] = useState("");

  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return options;
    return options.filter((o) => (o.display ?? o.value).toLowerCase().includes(needle));
  }, [options, query]);

  const showSearch = options.length >= SEARCHABLE_FROM;

  function toggle(value: string) {
    const next = new Set(hidden);
    if (next.has(value)) next.delete(value);
    else next.add(value);
    onChange(next);
  }

  function showAll() {
    onChange(new Set());
  }

  function hideAllVisible() {
    const next = new Set(hidden);
    visible.forEach((o) => next.add(o.value));
    onChange(next);
  }

  const hiddenCount = options.filter((o) => hidden.has(o.value)).length;

  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          className="inline-flex items-center gap-1.5 rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm text-gray-700 dark:text-gray-200 shadow-sm hover:border-blue-300 hover:text-blue-600 transition-colors"
          type="button"
        >
          {label}
          {hiddenCount > 0 ? (
            <span className="rounded bg-blue-50 px-1.5 py-0.5 text-[10px] font-medium text-blue-700 dark:bg-blue-950/40 dark:text-blue-400">
              {t("audit.facet.hiddenCount").replace("{count}", String(hiddenCount))}
            </span>
          ) : null}
          <svg className="h-3.5 w-3.5 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
          </svg>
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-80 p-0">
        {showSearch ? (
          <div className="border-b border-gray-100 dark:border-gray-800 p-2">
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t("audit.facet.searchPlaceholder")}
              className="w-full rounded-md border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-900 px-2 py-1 text-xs text-gray-900 dark:text-gray-100 focus:border-blue-500 focus:outline-none"
            />
          </div>
        ) : null}
        <div className="max-h-72 overflow-y-auto p-1">
          {visible.length === 0 ? (
            <p className="px-2 py-3 text-center text-xs text-gray-400 dark:text-gray-500">{t("audit.facet.noMatches")}</p>
          ) : (
            visible.map((o) => (
              <label
                key={o.value}
                className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 hover:bg-gray-50 dark:hover:bg-gray-800"
              >
                <input
                  type="checkbox"
                  checked={!hidden.has(o.value)}
                  onChange={() => toggle(o.value)}
                  className="h-3.5 w-3.5 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                />
                <span className="min-w-0 flex-1 truncate text-xs text-gray-700 dark:text-gray-200" title={o.display ?? o.value}>
                  {o.display ?? o.value}
                </span>
                {o.badge ? (
                  <span className={`rounded px-1 py-0.5 text-[9px] font-medium ${o.badgeClass ?? ""}`}>{o.badge}</span>
                ) : null}
                <span className="shrink-0 text-[10px] tabular-nums text-gray-400 dark:text-gray-500">{o.count}</span>
              </label>
            ))
          )}
        </div>
        <div className="flex items-center justify-between gap-2 border-t border-gray-100 dark:border-gray-800 p-2">
          <button
            type="button"
            onClick={showAll}
            className="rounded-md px-2 py-1 text-xs font-medium text-blue-600 hover:bg-blue-50 dark:hover:bg-blue-950/40"
          >
            {t("audit.facet.showAll")}
          </button>
          <button
            type="button"
            onClick={hideAllVisible}
            className="rounded-md px-2 py-1 text-xs font-medium text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-800"
          >
            {t("audit.facet.hideAll")}
          </button>
        </div>
      </PopoverContent>
    </Popover>
  );
}

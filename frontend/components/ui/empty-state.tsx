import * as React from "react";
import Link from "next/link";

/**
 * Standard empty state. Per the console redesign spec every empty state must
 * carry three things: an explanation, a primary next step, and a secondary
 * link (usually docs). This component enforces that shape so list pages stop
 * hand-rolling dashed boxes with inconsistent copy.
 */
export interface EmptyStateProps {
  icon?: React.ReactNode;
  title: string;
  description?: string;
  /** Primary next step. */
  action?: { label: string; href: string };
  /** Secondary link, e.g. docs. */
  secondary?: { label: string; href: string };
}

export function EmptyState({ icon, title, description, action, secondary }: EmptyStateProps) {
  return (
    <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 px-6 py-16 text-center">
      {icon && <div className="mb-3 text-gray-300 dark:text-gray-600">{icon}</div>}
      <p className="text-sm font-semibold text-gray-700 dark:text-gray-200">{title}</p>
      {description && <p className="mt-1 max-w-sm text-sm text-gray-500 dark:text-gray-400">{description}</p>}
      {(action || secondary) && (
        <div className="mt-5 flex items-center gap-3 text-sm">
          {action && (
            <Link
              href={action.href}
              className="inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 font-medium text-white transition-colors hover:bg-blue-700"
            >
              {action.label}
            </Link>
          )}
          {secondary && (
            <Link
              href={secondary.href}
              className="font-medium text-blue-600 hover:text-blue-700"
            >
              {secondary.label} →
            </Link>
          )}
        </div>
      )}
    </div>
  );
}

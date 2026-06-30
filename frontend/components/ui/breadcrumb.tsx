import Link from "next/link";

export interface Crumb {
  label: string;
  href?: string;
}

/**
 * Consistent breadcrumb trail. Replaces the hand-inlined `<Link>/<span>` blocks
 * that were duplicated across project pages. The last crumb renders as the
 * current page (no link).
 */
export function Breadcrumb({ items }: { items: Crumb[] }) {
  return (
    <nav aria-label="Breadcrumb" className="flex items-center gap-1.5 text-sm text-gray-500 dark:text-gray-400">
      {items.map((item, idx) => {
        const isLast = idx === items.length - 1;
        return (
          <span key={idx} className="flex items-center gap-1.5">
            {idx > 0 && <span className="text-gray-300 dark:text-gray-600">/</span>}
            {item.href && !isLast ? (
              <Link href={item.href} className="hover:text-gray-700 dark:hover:text-gray-200 transition-colors">
                {item.label}
              </Link>
            ) : (
              <span className={isLast ? "font-medium text-gray-900 dark:text-gray-100" : ""}>{item.label}</span>
            )}
          </span>
        );
      })}
    </nav>
  );
}

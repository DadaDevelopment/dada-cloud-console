"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useProjectContext } from "@/lib/project-context";
import { visibleNavItems, projectHref, type ResourceNavItem } from "@/lib/resources";
import { useT } from "@/lib/i18n/console/context";
import { ResourceIcon } from "./icons";

function isActive(pathname: string, projectId: string, item: ResourceNavItem): boolean {
  const href = projectHref(projectId, item);
  if (item.segment === "" && !item.absoluteHref) {
    // Overview matches the project root exactly (not its children).
    return pathname === `/projects/${projectId}` || pathname === href;
  }
  if (pathname === href || pathname.startsWith(href + "/")) return true;
  return (item.alsoMatches ?? []).some((seg) => {
    const alt = `/projects/${projectId}${seg}`;
    return pathname === alt || pathname.startsWith(alt + "/");
  });
}

/**
 * Project-scoped left navigation. Renders every resource type so the user can
 * jump between them in one click while inside a project — replacing the old
 * "return to the overview cards" round trip. Driven by the resource registry,
 * so new surfaces are one entry, not a layout change.
 */
export function ProjectNav() {
  const pathname = usePathname();
  const { t } = useT();
  const { projectId, role } = useProjectContext();
  if (!projectId) return null;

  const items = visibleNavItems(role);
  const resources = items.filter((i) => i.group === "resources");
  const infra = items.filter((i) => i.group === "infra");
  const admin = items.filter((i) => i.group === "admin");

  function renderItem(item: ResourceNavItem) {
    const active = isActive(pathname, projectId!, item);
    const cls = `flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors ${
      active ? "bg-blue-600 text-white" : "text-slate-200 hover:bg-slate-800 hover:text-white"
    }`;

    if (item.comingSoon) {
      return (
        <div
          key={item.key}
          title={t("shell.nav.comingSoon")}
          aria-disabled="true"
          className="flex cursor-default items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium text-slate-500"
        >
          <ResourceIcon name={item.icon} className="h-4 w-4 shrink-0" />
          <span className="flex-1">{t(`nav.${item.key}`)}</span>
          <span className="rounded bg-slate-800 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-slate-400">{t("shell.nav.soon")}</span>
        </div>
      );
    }

    return (
      <Link key={item.key} href={projectHref(projectId!, item)} className={cls} aria-current={active ? "page" : undefined}>
        <ResourceIcon name={item.icon} className="h-4 w-4 shrink-0" />
        {t(`nav.${item.key}`)}
      </Link>
    );
  }

  return (
    <nav className="flex-1 space-y-1 overflow-y-auto px-3 py-4">
      {resources.map(renderItem)}
      {infra.length > 0 && (
        <>
          <div className="px-3 pb-1 pt-4 text-[10px] font-semibold uppercase tracking-wider text-slate-500">{t("shell.nav.advanced")}</div>
          {infra.map(renderItem)}
        </>
      )}
      {admin.length > 0 && (
        <>
          <div className="px-3 pb-1 pt-4 text-[10px] font-semibold uppercase tracking-wider text-slate-500">{t("shell.nav.admin")}</div>
          {admin.map(renderItem)}
        </>
      )}
    </nav>
  );
}

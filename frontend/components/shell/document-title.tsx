"use client";
import { useEffect } from "react";
import { usePathname } from "next/navigation";
import { useProjectContext } from "@/lib/project-context";
import { useT } from "@/lib/i18n/console/context";
import { describePath } from "@/lib/page-title";

/**
 * Keeps the tab title in step with the route on client navigation.
 *
 * The server renders the same title into the initial HTML (app/layout.tsx), but
 * that metadata belongs to the root layout, which does not re-render when only a
 * child segment changes — so without this, every in-app navigation would leave
 * the first page's title in the tab. It also adds the project name, which the
 * server cannot resolve without a session.
 */
export function DocumentTitle() {
  const pathname = usePathname();
  const { project } = useProjectContext();
  const { locale } = useT();

  useEffect(() => {
    document.title = describePath(pathname, locale, project?.name).title;
  }, [pathname, locale, project?.name]);

  return null;
}

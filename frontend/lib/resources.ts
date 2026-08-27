/**
 * Declarative registry of project-scoped resources.
 *
 * The project left-nav and the palette are both rendered from this list, so a
 * new surface is a single entry here rather than a layout change. Items can be
 * gated by capability (`visible`) and flagged `comingSoon` to render disabled
 * with a tooltip rather than being silently hidden.
 */

import type { MemberRole } from "./types";

export type IconName =
  | "overview"
  | "apps"
  | "databases"
  | "models"
  | "ai"
  | "app-servers"
  | "boxes"
  | "operations"
  | "approvals"
  | "redis"
  | "queue"
  | "storage"
  | "git"
  | "domains"
  | "monitoring"
  | "members"
  | "billing";

export interface ResourceNavItem {
  key: string;
  label: string;
  icon: IconName;
  /** Path suffix appended after /projects/[projectId]. "" = overview. */
  segment: string;
  /**
   * Extra path suffixes this item owns for highlighting purposes. Used when one
   * nav entry fronts several routes (Data = databases + object storage), so the
   * sidebar still marks itself active on the sibling route.
   */
  alsoMatches?: string[];
  group: "resources" | "infra" | "admin";
  /** Absolute path that ignores the project scope (e.g. global admin pages). */
  absoluteHref?: string;
  /** When false the item is hidden entirely. Defaults to always-visible. */
  visible?: (role: MemberRole | undefined) => boolean;
  comingSoon?: boolean;
}

/**
 * Sidebar registry, deliberately short.
 *
 * Primary group is the task path: GitHub -> app -> data -> AI. Databases and
 * object storage share one "Data" entry fronting two tabs, since both answer
 * "where does my app keep things" and neither was a destination on its own.
 * Domains, Inference and Managed VM sit under "Advanced".
 *
 * Monitoring, Builds, Boxes, Operations, Approvals, Redis, Message Queues,
 * Members and Billing hold no sidebar slot: telemetry over 07-31..08-02 (841
 * events, 24 identities) recorded zero navigations and zero clicks on
 * Monitoring, Builds and Inference, and two clicks on Boxes. Every route stays
 * reachable -- Monitoring and Builds from the overview's secondary links and
 * from post-action redirects, Boxes as a read-only overview panel (creating and
 * driving a box is an agent/MCP job), Members and Billing from the account menu.
 */
export const PROJECT_NAV: ResourceNavItem[] = [
  { key: "overview", label: "Overview", icon: "overview", segment: "", group: "resources" },
  { key: "apps", label: "Applications", icon: "apps", segment: "/apps", group: "resources" },
  { key: "data", label: "Databases & S3", icon: "databases", segment: "/databases", alsoMatches: ["/storage", "/redis"], group: "resources" },
  { key: "ai", label: "AI API", icon: "ai", segment: "/ai", group: "resources" },
  { key: "agents", label: "Agents", icon: "ai", segment: "/agents", group: "resources" },
  { key: "domains", label: "Domains", icon: "domains", segment: "/domains", group: "infra" },
  { key: "models", label: "AI Models", icon: "models", segment: "/models", group: "infra" },
  { key: "app-servers", label: "App Servers", icon: "app-servers", segment: "/app-servers", group: "infra" },
];
export function projectHref(projectId: string, item: ResourceNavItem): string {
  return item.absoluteHref ?? `/projects/${projectId}${item.segment}`;
}

export function visibleNavItems(role: MemberRole | undefined): ResourceNavItem[] {
  return PROJECT_NAV.filter((i) => (i.visible ? i.visible(role) : true));
}

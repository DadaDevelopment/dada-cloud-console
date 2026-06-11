// Declarative registry of project-scoped resources.
//
// The project left-nav and the ⌘K command palette are both rendered from this
// list. Adding a v2–v4 surface (Redis, RabbitMQ, Object Storage, Marketplace,
// Cost, Observability) is a single entry here — no layout rewrite. Items can be
// gated by capability (`visible`) and flagged `comingSoon` to render disabled
// with a tooltip rather than being silently hidden.

import type { MemberRole } from "./types";
import { canApprove } from "./rbac";

export type IconName =
  | "overview"
  | "apps"
  | "databases"
  | "models"
  | "app-servers"
  | "operations"
  | "approvals"
  | "redis"
  | "queue"
  | "storage";

export interface ResourceNavItem {
  key: string;
  label: string;
  icon: IconName;
  /** Path suffix appended after /projects/[projectId]. "" = overview. */
  segment: string;
  group: "resources" | "admin";
  /** Absolute path that ignores the project scope (e.g. global admin pages). */
  absoluteHref?: string;
  /** When false the item is hidden entirely. Defaults to always-visible. */
  visible?: (role: MemberRole | undefined) => boolean;
  comingSoon?: boolean;
}

export const PROJECT_NAV: ResourceNavItem[] = [
  { key: "overview", label: "Overview", icon: "overview", segment: "", group: "resources" },
  { key: "apps", label: "Applications", icon: "apps", segment: "/apps", group: "resources" },
  { key: "databases", label: "Databases", icon: "databases", segment: "/databases", group: "resources" },
  { key: "models", label: "AI Models", icon: "models", segment: "/models", group: "resources" },
  { key: "app-servers", label: "App Servers", icon: "app-servers", segment: "/app-servers", group: "resources" },
  { key: "operations", label: "Operations", icon: "operations", segment: "/operations", group: "resources" },
  // --- roadmap placeholders (v2–v4): visible, disabled-with-tooltip ---
  { key: "redis", label: "Redis", icon: "redis", segment: "/redis", group: "resources", comingSoon: true },
  { key: "queues", label: "Message Queues", icon: "queue", segment: "/queues", group: "resources", comingSoon: true },
  { key: "storage", label: "Object Storage", icon: "storage", segment: "/storage", group: "resources", comingSoon: true },
  // --- admin group (global, cross-project) ---
  { key: "approvals", label: "Approvals", icon: "approvals", segment: "", absoluteHref: "/admin/approvals", group: "admin", visible: canApprove },
];

export function projectHref(projectId: string, item: ResourceNavItem): string {
  return item.absoluteHref ?? `/projects/${projectId}${item.segment}`;
}

export function visibleNavItems(role: MemberRole | undefined): ResourceNavItem[] {
  return PROJECT_NAV.filter((i) => (i.visible ? i.visible(role) : true));
}

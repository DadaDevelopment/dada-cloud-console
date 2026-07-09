// Declarative registry of project-scoped resources.
//
// The project left-nav and the ⌘K command palette are both rendered from this
// list. Adding a v2–v4 surface (Redis, RabbitMQ, Object Storage, Marketplace,
// Cost, Observability) is a single entry here — no layout rewrite. Items can be
// gated by capability (`visible`) and flagged `comingSoon` to render disabled
// with a tooltip rather than being silently hidden.

import type { MemberRole } from "./types";
import { canSeeManifests, isAdmin } from "./rbac";

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
  group: "resources" | "infra" | "admin";
  /** Absolute path that ignores the project scope (e.g. global admin pages). */
  absoluteHref?: string;
  /** When false the item is hidden entirely. Defaults to always-visible. */
  visible?: (role: MemberRole | undefined) => boolean;
  comingSoon?: boolean;
}

export const PROJECT_NAV: ResourceNavItem[] = [
  // --- primary, task-oriented surfaces: the GitHub→app→db→domain→logs path.
  // Kept short on purpose so the first screen reads as "what do I do next",
  // not "here is a catalog of infra entities".
  { key: "overview", label: "Overview", icon: "overview", segment: "", group: "resources" },
  { key: "apps", label: "Applications", icon: "apps", segment: "/apps", group: "resources" },
  { key: "databases", label: "Databases", icon: "databases", segment: "/databases", group: "resources" },
  { key: "storage", label: "Object Storage", icon: "storage", segment: "/storage", group: "resources" },
  { key: "domains", label: "Domains", icon: "domains", segment: "/domains", group: "resources" },
  { key: "monitoring", label: "Monitoring", icon: "monitoring", segment: "/monitoring", group: "resources" },
  // --- advanced / infrastructure: still one click away, but folded under an
  // "Advanced" header so they don't crowd the primary path.
  { key: "models", label: "AI Models", icon: "models", segment: "/models", group: "infra" },
  { key: "app-servers", label: "Managed VM", icon: "app-servers", segment: "/app-servers", group: "infra" },
  { key: "git", label: "Builds", icon: "git", segment: "/git", group: "infra", visible: canSeeManifests },
  // Operations, Approvals, Redis and Message Queues were removed from the sidebar
  // to keep the nav to real, task-oriented surfaces. Operations still runs — its
  // route stays reachable via post-action redirects and deep links — it just no
  // longer occupies a top-level nav slot.
  // --- admin group (global, cross-project) ---
  { key: "members", label: "Members", icon: "members", segment: "/members", group: "admin", visible: isAdmin },
  { key: "billing", label: "Billing", icon: "billing", segment: "/billing", group: "admin", visible: isAdmin },
];

export function projectHref(projectId: string, item: ResourceNavItem): string {
  return item.absoluteHref ?? `/projects/${projectId}${item.segment}`;
}

export function visibleNavItems(role: MemberRole | undefined): ResourceNavItem[] {
  return PROJECT_NAV.filter((i) => (i.visible ? i.visible(role) : true));
}

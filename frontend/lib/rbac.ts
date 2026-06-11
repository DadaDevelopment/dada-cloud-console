// Role-based capability predicates.
//
// Roles in Dada are PROJECT-SCOPED (see ProjectDetailResponse.role), not a
// global property of the user. Every capability check therefore takes the
// MemberRole the user holds *in the current project*. The UI uses these to
// decide how much depth to disclose per the PRD's three-persona model:
//   - client (client-admin/client-viewer): no infra, no YAML, no approvals
//   - developer: + technical panels and observability links, no raw YAML
//   - platform-admin: + manifests, raw compose/values editors, approvals
//
// Keep these as the single source of truth — never branch on a raw role
// string inside a component.

import type { MemberRole } from "./types";

export const roleLabels: Record<MemberRole, string> = {
  "platform-admin": "Platform Admin",
  developer: "Developer",
  "client-admin": "Admin",
  "client-viewer": "Viewer",
};

export const roleColors: Record<MemberRole, string> = {
  "platform-admin": "bg-purple-100 text-purple-700",
  developer: "bg-blue-100 text-blue-700",
  "client-admin": "bg-green-100 text-green-700",
  "client-viewer": "bg-gray-100 text-gray-600",
};

function is(role: MemberRole | undefined, ...roles: MemberRole[]): boolean {
  return role !== undefined && roles.includes(role);
}

/** Platform admin — sees everything, approves dangerous ops. */
export function isAdmin(role: MemberRole | undefined): boolean {
  return is(role, "platform-admin");
}

/** Internal users (developer + admin) who can see technical detail. */
export function isInternal(role: MemberRole | undefined): boolean {
  return is(role, "platform-admin", "developer");
}

/**
 * Technical surfaces: image digests, replicas/profile, Argo sync state,
 * out-links to Grafana/logs/OpenAPI. Developers and admins only.
 */
export function canSeeTechnical(role: MemberRole | undefined): boolean {
  return isInternal(role);
}

/**
 * Raw GitOps internals: compose/values YAML editors, the Manifests tab,
 * git commit/path. Platform admins only — clients must never see these,
 * and developers self-serve through typed actions, not raw YAML.
 */
export function canSeeManifests(role: MemberRole | undefined): boolean {
  return isAdmin(role);
}

export const canEditYaml = canSeeManifests;

/** Approvals nav + page — platform admins only. */
export function canApprove(role: MemberRole | undefined): boolean {
  return isAdmin(role);
}

/** Any write action. Everyone except read-only viewers. */
export function canMutate(role: MemberRole | undefined): boolean {
  return role !== undefined && role !== "client-viewer";
}

// Role-based capability predicates.
//
// Roles in Dada are PROJECT-SCOPED (see ProjectDetailResponse.role) and follow
// the uniform 4-role ladder (ADR-009): Owner > Admin > Developer > ReadOnly.
// The effective role is computed server-side as max(org_role, projects[id]) and
// handed to the UI per project. These predicates mirror the backend authz:
//   - isOrgAdmin (Owner|Admin): manifests, approvals, dangerous ops
//   - canWrite   (Owner|Admin|Developer): any mutation, technical surfaces
//   - ReadOnly: read-only, no infra/YAML/approvals
//
// Keep these as the single source of truth — never branch on a raw role
// string inside a component.

import type { MemberRole } from "./types";

export const roleLabels: Record<MemberRole, string> = {
  Owner: "Owner",
  Admin: "Admin",
  Developer: "Developer",
  ReadOnly: "Read Only",
};

export const roleColors: Record<MemberRole, string> = {
  Owner: "bg-purple-100 text-purple-700",
  Admin: "bg-green-100 text-green-700",
  Developer: "bg-blue-100 text-blue-700",
  ReadOnly: "bg-gray-100 text-gray-600",
};

function is(role: MemberRole | undefined, ...roles: MemberRole[]): boolean {
  return role !== undefined && roles.includes(role);
}

/** Org admin — Owner or Admin. Sees everything, approves dangerous ops. */
export function isAdmin(role: MemberRole | undefined): boolean {
  return is(role, "Owner", "Admin");
}

/** Non-readonly members who can see technical detail. */
export function isInternal(role: MemberRole | undefined): boolean {
  return is(role, "Owner", "Admin", "Developer");
}

/**
 * Technical surfaces: image digests, replicas/profile, Argo sync state,
 * out-links to Grafana/logs/OpenAPI. Everyone except ReadOnly.
 */
export function canSeeTechnical(role: MemberRole | undefined): boolean {
  return isInternal(role);
}

/**
 * Raw GitOps internals: compose/values YAML editors, the Manifests tab,
 * git commit/path. Owners and Admins only.
 */
export function canSeeManifests(role: MemberRole | undefined): boolean {
  return isAdmin(role);
}

export const canEditYaml = canSeeManifests;

/** Approvals nav + page — Owners and Admins only. */
export function canApprove(role: MemberRole | undefined): boolean {
  return isAdmin(role);
}

/** Any write action. Everyone except ReadOnly. */
export function canMutate(role: MemberRole | undefined): boolean {
  return isInternal(role);
}

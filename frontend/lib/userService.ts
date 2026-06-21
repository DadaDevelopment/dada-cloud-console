// user-service client (ADR-009 / PRD-IAM).
//
// user-service (Java/Spring) is the single authority for orgs, membership,
// roles, invitations and service accounts. dada-cloud owns NONE of it. The
// console talks to user-service directly; these endpoints sit at the gateway
// ROOT (not under dada-cloud's /api/v1). Same Keycloak bearer token authorizes
// both, so we reuse apiFetch's token plumbing and only swap the base URL.
//
// Base URL resolution:
//   NEXT_PUBLIC_USER_SERVICE_URL  → explicit (non-prod / split deploys)
//   ""                            → relative, assumes the gateway proxies
//                                   /orgs, /projects/*/members at the same origin
//
// Contract is taken verbatim from PRD-IAM "API surface (new)". If the gateway
// mounts these under a prefix (e.g. /iam), set NEXT_PUBLIC_USER_SERVICE_URL or
// adjust USER_SERVICE_BASE — confirm the public path with the gateway chip.

import { apiFetch } from "./api";
import type { Invitation, Member, MemberRole, Org } from "./types";

const USER_SERVICE_BASE = process.env.NEXT_PUBLIC_USER_SERVICE_URL ?? "";

function us<T>(path: string, init?: { method?: string; body?: unknown }): Promise<T> {
  return apiFetch<T>(path, { ...init, baseUrl: USER_SERVICE_BASE });
}

export const userServiceApi = {
  // ── Orgs ──────────────────────────────────────────────────────────────
  getOrg: (orgId: string) => us<Org>(`/orgs/${orgId}`),

  // ── Org members ───────────────────────────────────────────────────────
  listOrgMembers: (orgId: string) =>
    us<{ members: Member[] }>(`/orgs/${orgId}/members`),
  // add-existing shortcut (already-registered user, no token round-trip)
  addOrgMember: (orgId: string, email: string, role: MemberRole) =>
    us<Member>(`/orgs/${orgId}/members`, { method: "POST", body: { email, role } }),
  changeOrgMemberRole: (orgId: string, principalId: string, role: MemberRole) =>
    us<Member>(`/orgs/${orgId}/members/${principalId}`, { method: "POST", body: { role } }),
  removeOrgMember: (orgId: string, principalId: string) =>
    us<void>(`/orgs/${orgId}/members/${principalId}`, { method: "DELETE" }),

  // ── Invitations ───────────────────────────────────────────────────────
  invite: (orgId: string, email: string, role: MemberRole) =>
    us<Invitation>(`/orgs/${orgId}/invitations`, { method: "POST", body: { email, role } }),

  // ── Project members ───────────────────────────────────────────────────
  listProjectMembers: (projectId: string) =>
    us<{ members: Member[] }>(`/projects/${projectId}/members`),
  addProjectMember: (projectId: string, email: string, role: MemberRole) =>
    us<Member>(`/projects/${projectId}/members`, { method: "POST", body: { email, role } }),
  removeProjectMember: (projectId: string, principalId: string) =>
    us<void>(`/projects/${projectId}/members/${principalId}`, { method: "DELETE" }),
};

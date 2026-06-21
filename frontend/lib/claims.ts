// Decode NATIVE Keycloak RBAC claims (ADR-009 §2-4) on the client for UI-only
// purposes.
//
// IMPORTANT: this does NOT verify the token. It only base64url-decodes the
// payload so the console can gate UI. All real authz is enforced server-side from
// the signed claims — never trust these for security.
//
// Keycloak emits stock claims; there is no pre-shaped org_role/projects claim:
//   groups: ["/orgs/acme/Admin", "/orgs/acme/projects/p1/Developer", "/platform-admins"?]
//   scope:  "read metrics:write logs:write ..."   (space-delimited, standard OIDC)
//
// We decode group paths into org→role / project→role / project→org maps and split
// the scope string into a set, mirroring the backend decoder so the UI agrees
// with server authz.

import { useMemo } from "react";
import { useAuth } from "./auth-provider";
import type { MemberRole } from "./types";

const PLATFORM_ADMINS = "/platform-admins";

export interface DecodedClaims {
  /** org id → caller's role in that org. */
  orgRoles: Record<string, MemberRole>;
  /** project id → caller's explicit role on that project. */
  projectRoles: Record<string, MemberRole>;
  /** project id → owning org id (only for projects carried as group paths). */
  projectOrg: Record<string, string>;
  /** native OIDC scopes. */
  scopes: string[];
  /** /platform-admins staff god-mode (outside the role enum). */
  platformAdmin: boolean;
}

interface RawTokenClaims {
  groups?: string[];
  scope?: string;
}

function base64UrlDecode(input: string): string {
  const pad = input.length % 4 === 0 ? "" : "=".repeat(4 - (input.length % 4));
  const b64 = input.replace(/-/g, "+").replace(/_/g, "/") + pad;
  if (typeof atob === "function") return atob(b64);
  // SSR fallback
  return Buffer.from(b64, "base64").toString("binary");
}

const ROLE_RANK: Record<string, number> = {
  Owner: 4,
  Admin: 3,
  Developer: 2,
  ReadOnly: 1,
};

function higher(a: MemberRole | undefined, b: MemberRole): MemberRole {
  if (!a) return b;
  return (ROLE_RANK[b] ?? 0) > (ROLE_RANK[a] ?? 0) ? b : a;
}

/** Decode native group paths + scope into the lookup maps (UI-only, unverified). */
export function decodeClaims(token: string | null | undefined): DecodedClaims | null {
  if (!token) return null;
  const parts = token.split(".");
  if (parts.length < 2) return null;

  let raw: RawTokenClaims;
  try {
    raw = JSON.parse(base64UrlDecode(parts[1])) as RawTokenClaims;
  } catch {
    return null;
  }

  const out: DecodedClaims = {
    orgRoles: {},
    projectRoles: {},
    projectOrg: {},
    scopes: [],
    platformAdmin: false,
  };

  for (const g of raw.groups ?? []) {
    if (g === PLATFORM_ADMINS) {
      out.platformAdmin = true;
      continue;
    }
    // "/orgs/{org}/{Role}"                  → org role
    // "/orgs/{org}/projects/{proj}/{Role}"  → project role
    const seg = g.replace(/^\/+|\/+$/g, "").split("/");
    if (seg.length < 3 || seg[0] !== "orgs") continue;
    if (seg.length === 3) {
      const [, org, role] = seg;
      out.orgRoles[org] = higher(out.orgRoles[org], role as MemberRole);
    } else if (seg.length === 5 && seg[2] === "projects") {
      const [, org, , proj, role] = seg;
      out.projectRoles[proj] = higher(out.projectRoles[proj], role as MemberRole);
      out.projectOrg[proj] = org;
    }
  }

  out.scopes = (raw.scope ?? "").split(/\s+/).filter(Boolean);
  return out;
}

/** Active decoded claims from the current bearer token (UI-only, unverified). */
export function useClaims(): DecodedClaims | null {
  const { token } = useAuth();
  return useMemo(() => decodeClaims(token), [token]);
}

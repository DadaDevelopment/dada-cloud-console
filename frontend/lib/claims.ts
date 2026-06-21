// Read fat-JWT claims (ADR-009) on the client for UI purposes only.
//
// IMPORTANT: this does NOT verify the token. It only base64url-decodes the
// payload so the console can show the active org and gate UI. All real authz is
// enforced server-side from the signed claims — never trust these for security.

import { useMemo } from "react";
import { useAuth } from "./auth-provider";
import type { MemberRole } from "./types";

export interface FatClaims {
  org_id?: string;
  org_role?: MemberRole;
  projects?: Record<string, MemberRole>;
  scopes?: string[];
}

function base64UrlDecode(input: string): string {
  const pad = input.length % 4 === 0 ? "" : "=".repeat(4 - (input.length % 4));
  const b64 = input.replace(/-/g, "+").replace(/_/g, "/") + pad;
  if (typeof atob === "function") return atob(b64);
  // SSR fallback
  return Buffer.from(b64, "base64").toString("binary");
}

export function decodeClaims(token: string | null | undefined): FatClaims | null {
  if (!token) return null;
  const parts = token.split(".");
  if (parts.length < 2) return null;
  try {
    return JSON.parse(base64UrlDecode(parts[1])) as FatClaims;
  } catch {
    return null;
  }
}

/** Active fat claims from the current bearer token (UI-only, unverified). */
export function useClaims(): FatClaims | null {
  const { token } = useAuth();
  return useMemo(() => decodeClaims(token), [token]);
}

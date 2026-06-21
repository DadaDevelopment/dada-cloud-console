"use client";
// Org switcher (PRD-IAM "Org switcher, top nav").
//
// SCOPE NOTE: the fat JWT carries exactly ONE active org (org_id/org_role), and
// PRD-IAM exposes GET /orgs/{id} but no "list my orgs" endpoint. So this renders
// the ACTIVE org only (read-only chip). True multi-org switching is blocked on:
//   1. a user-service "list orgs for principal" endpoint, and
//   2. a token re-mint flow (switching org changes the fat claims), owned by the
//      gateway/auth chip.
// TODO(iam): turn this into a dropdown once those two land.

import { useEffect, useState } from "react";
import { useClaims } from "@/lib/claims";
import { userServiceApi } from "@/lib/userService";
import { roleLabels } from "@/lib/rbac";
import type { Org } from "@/lib/types";

export function OrgSwitcher() {
  const claims = useClaims();
  const orgId = claims?.org_id;
  const [org, setOrg] = useState<Org | null>(null);

  useEffect(() => {
    if (!orgId) return;
    let active = true;
    userServiceApi
      .getOrg(orgId)
      .then((o) => { if (active) setOrg(o); })
      .catch(() => { /* display-only; fall back to the id */ });
    return () => { active = false; };
  }, [orgId]);

  if (!orgId) return null;

  const label = org?.display_name ?? org?.slug ?? orgId;
  const role = claims?.org_role;

  return (
    <div
      className="flex items-center gap-2 rounded-lg border border-slate-700 bg-slate-800 px-3 py-1.5 text-sm text-slate-200"
      title="Active organization (switching coming soon)"
    >
      <svg className="h-4 w-4 text-slate-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5} aria-hidden="true">
        <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 21h16.5M4.5 3h15M5.25 3v18m13.5-18v18M9 6.75h1.5m-1.5 3h1.5m-1.5 3h1.5m3-6H15m-1.5 3H15m-1.5 3H15M9 21v-3.375c0-.621.504-1.125 1.125-1.125h3.75c.621 0 1.125.504 1.125 1.125V21" />
      </svg>
      <span className="font-medium">{label}</span>
      {role && <span className="text-xs text-slate-400">{roleLabels[role] ?? role}</span>}
    </div>
  );
}

"use client";
// Project members management (PRD-IAM "Members page").
//
// Membership is owned by user-service (ADR-009), so every read/write here goes
// to userServiceApi, NOT dada-cloud. dada-cloud only holds the resource row.
// Org-admin-only surface: the nav entry is gated by isAdmin and we re-check the
// active project role here so a deep link can't bypass it.

import { useCallback, useEffect, useState, FormEvent } from "react";
import { useParams } from "next/navigation";
import { useProjectContext } from "@/lib/project-context";
import { useClaims } from "@/lib/claims";
import { userServiceApi } from "@/lib/userService";
import { roleColors, roleLabels, isAdmin } from "@/lib/rbac";
import type { Member, MemberRole } from "@/lib/types";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Modal } from "@/components/ui/modal";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";

const ROLES: MemberRole[] = ["Owner", "Admin", "Developer", "ReadOnly"];

function RolePill({ role }: { role: MemberRole }) {
  return (
    <span className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${roleColors[role] ?? "bg-gray-100 text-gray-600"}`}>
      {roleLabels[role] ?? role}
    </span>
  );
}

export default function MembersPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const { project, role } = useProjectContext();
  const claims = useClaims();
  // The invite is org-scoped. The project's owning org is authoritative (ADR-009
  // multi-org: there is no single "active org" in the token); fall back to the
  // token's project→org decode when the project row hasn't loaded yet.
  const orgId = project?.org_id || claims?.projectOrg[projectId];

  const [members, setMembers] = useState<Member[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  // Add/invite modal
  const [showAdd, setShowAdd] = useState(false);
  const [email, setEmail] = useState("");
  const [addRole, setAddRole] = useState<MemberRole>("Developer");
  const [sendInvite, setSendInvite] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const load = useCallback(async () => {
    setActionError(null);
    try {
      const data = await userServiceApi.listProjectMembers(projectId);
      setMembers(data.members ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load members");
    } finally {
      setLoading(false);
    }
  }, [projectId]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- fetch-on-mount; userService is the data source, no Suspense boundary above this client component.
    load();
  }, [load]);

  async function changeRole(m: Member, next: MemberRole) {
    setBusyId(m.principal_id);
    setActionError(null);
    try {
      // POST upserts the membership role (PRD: add-existing is the role writer).
      await userServiceApi.addProjectMember(projectId, m.email, next);
      setMembers((rows) => rows.map((r) => (r.principal_id === m.principal_id ? { ...r, role: next } : r)));
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Failed to change role");
    } finally {
      setBusyId(null);
    }
  }

  async function remove(m: Member) {
    if (!confirm(`Remove ${m.email} from this project?`)) return;
    setBusyId(m.principal_id);
    setActionError(null);
    try {
      await userServiceApi.removeProjectMember(projectId, m.principal_id);
      setMembers((rows) => rows.filter((r) => r.principal_id !== m.principal_id));
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Failed to remove member");
    } finally {
      setBusyId(null);
    }
  }

  async function submitAdd(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitting(true);
    setActionError(null);
    try {
      if (sendInvite) {
        // Email invitation is org-scoped (PRD): resolves on the invitee's first
        // registration. Needs the active org id from the fat claim.
        if (!orgId) throw new Error("No active org in token — cannot send invite");
        await userServiceApi.invite(orgId, email, addRole);
      } else {
        // Add-existing shortcut: instantly add an already-registered user.
        await userServiceApi.addProjectMember(projectId, email, addRole);
      }
      setShowAdd(false);
      setEmail("");
      setAddRole("Developer");
      setSendInvite(false);
      await load();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Failed to add member");
    } finally {
      setSubmitting(false);
    }
  }

  if (!isAdmin(role)) {
    return (
      <div className="p-8">
        <p className="text-sm text-gray-500">You need an Owner or Admin role to manage members.</p>
      </div>
    );
  }

  const columns: Column<Member>[] = [
    {
      key: "member",
      header: "Member",
      sortValue: (m) => m.display_name || m.email,
      render: (m) => (
        <div>
          <div className="font-medium text-gray-900">{m.display_name || m.email}</div>
          <div className="text-xs text-gray-500">{m.email}</div>
        </div>
      ),
    },
    {
      key: "type",
      header: "Type",
      render: (m) => (
        <span className="text-xs text-gray-500">{m.principal_type === "service_account" ? "Service account" : "User"}</span>
      ),
    },
    {
      key: "role",
      header: "Role",
      sortValue: (m) => m.role,
      render: (m) => (
        <div className="flex items-center gap-2">
          <RolePill role={m.role} />
          <select
            aria-label={`Role for ${m.email}`}
            value={m.role}
            disabled={busyId === m.principal_id}
            onChange={(e) => changeRole(m, e.target.value as MemberRole)}
            className="rounded border border-gray-200 bg-white px-1.5 py-0.5 text-xs"
          >
            {ROLES.map((r) => (
              <option key={r} value={r}>{roleLabels[r]}</option>
            ))}
          </select>
        </div>
      ),
    },
    {
      key: "actions",
      header: "",
      align: "right",
      render: (m) => (
        <Button variant="ghost" size="sm" disabled={busyId === m.principal_id} onClick={() => remove(m)}>
          Remove
        </Button>
      ),
    },
  ];

  return (
    <div className="p-8">
      <Breadcrumb
        items={[
          { label: "Projects", href: "/projects" },
          { label: project?.display_name ?? projectId, href: `/projects/${projectId}` },
          { label: "Members" },
        ]}
      />

      <div className="mb-6 mt-2 flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-gray-900">Members</h1>
          <p className="text-sm text-gray-500">Membership is managed by your organization. Roles control access across this project.</p>
        </div>
        <Button onClick={() => setShowAdd(true)}>Add member</Button>
      </div>

      {error ? (
        <div className="rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700">{error}</div>
      ) : (
        <>
          {actionError && (
            <div className="mb-4 rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700">{actionError}</div>
          )}
          <DataTable
            columns={columns}
            rows={members}
            getRowKey={(m) => m.principal_id}
            searchText={(m) => `${m.display_name} ${m.email} ${m.role}`}
            searchPlaceholder="Search members…"
            loading={loading}
            emptyState={<div className="py-12 text-center text-sm text-gray-500">No members yet.</div>}
          />
        </>
      )}

      <Modal isOpen={showAdd} onClose={() => setShowAdd(false)} title="Add member">
        <form onSubmit={submitAdd} className="space-y-4">
          <div>
            <label className="mb-1 block text-sm font-medium text-gray-700">Email</label>
            <input
              type="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="teammate@company.com"
              className="w-full rounded-lg border border-gray-300 px-3 py-2 text-sm"
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium text-gray-700">Role</label>
            <select
              value={addRole}
              onChange={(e) => setAddRole(e.target.value as MemberRole)}
              className="w-full rounded-lg border border-gray-300 px-3 py-2 text-sm"
            >
              {ROLES.map((r) => (
                <option key={r} value={r}>{roleLabels[r]}</option>
              ))}
            </select>
          </div>
          <label className="flex items-start gap-2 text-sm text-gray-700">
            <input type="checkbox" checked={sendInvite} onChange={(e) => setSendInvite(e.target.checked)} className="mt-0.5" />
            <span>
              Send email invitation
              <span className="block text-xs text-gray-500">
                For users who haven&apos;t registered yet. Otherwise the existing user is added immediately.
                {sendInvite && !orgId && <span className="text-red-600"> No active org in your session — invite unavailable.</span>}
              </span>
            </span>
          </label>
          {actionError && <div className="text-sm text-red-600">{actionError}</div>}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={() => setShowAdd(false)}>Cancel</Button>
            <Button type="submit" isLoading={submitting} disabled={submitting || (sendInvite && !orgId)}>
              {sendInvite ? "Send invite" : "Add"}
            </Button>
          </div>
        </form>
      </Modal>

      {loading && members.length === 0 && !error && (
        <div className="mt-4 flex justify-center"><Spinner /></div>
      )}
    </div>
  );
}

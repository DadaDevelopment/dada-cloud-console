"use client";

import { useCallback, useEffect, useState, FormEvent } from "react";
import { useParams, useRouter } from "next/navigation";
import { useProjectContext } from "@/lib/project-context";
import { useClaims } from "@/lib/claims";
import { userServiceApi } from "@/lib/userService";
import { roleColors, isAdmin } from "@/lib/rbac";
import type { Member, MemberRole } from "@/lib/types";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Modal } from "@/components/ui/modal";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";
import { DeleteImpactModal, deleteImpactTargetKey, type DeleteImpactTarget } from "@/components/resources/delete-impact-modal";

const ROLES: MemberRole[] = ["Owner", "Admin", "Developer", "ReadOnly"];

function RolePill({ role }: { role: MemberRole }) {
  const { t } = useT();
  return (
    <span className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${roleColors[role] ?? "bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-400"}`}>
      {t(`roles.${role}`)}
    </span>
  );
}

export default function MembersPage() {
  const params = useParams<{ projectId: string }>();
  const router = useRouter();
  const projectId = params.projectId;
  const { project, role, refetchProjects } = useProjectContext();
  const claims = useClaims();
  const { t } = useT();
  const orgId = project?.org_id || claims?.projectOrg[projectId];

  const [deleteTarget, setDeleteTarget] = useState<DeleteImpactTarget | null>(null);

  const [members, setMembers] = useState<Member[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

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
      setError(null);
    } catch (err) {
      const msg = err instanceof Error ? err.message : t("members.error.load");
      for (let attempt = 0; attempt < 2; attempt++) {
        try {
          await new Promise((r) => setTimeout(r, 1500 * (attempt + 1)));
          const data = await userServiceApi.listProjectMembers(projectId);
          setMembers(data.members ?? []);
          setError(null);
          return;
        } catch (retryErr) {
          void retryErr;
        }
      }
      setError(msg);
    } finally {
      setLoading(false);
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- fetch-on-mount; userService is the data source, no Suspense boundary above this client component.
    load();
  }, [load]);

  async function changeRole(m: Member, next: MemberRole) {
    setBusyId(m.principal_id);
    setActionError(null);
    try {
      await userServiceApi.addProjectMember(projectId, m.email, next);
      setMembers((rows) => rows.map((r) => (r.principal_id === m.principal_id ? { ...r, role: next } : r)));
    } catch (err) {
      setActionError(err instanceof Error ? err.message : t("members.error.changeRole"));
    } finally {
      setBusyId(null);
    }
  }

  async function remove(m: Member) {
    if (!confirm(t("members.confirm.remove", { email: m.email }))) return;
    setBusyId(m.principal_id);
    setActionError(null);
    try {
      await userServiceApi.removeProjectMember(projectId, m.principal_id);
      setMembers((rows) => rows.filter((r) => r.principal_id !== m.principal_id));
    } catch (err) {
      setActionError(err instanceof Error ? err.message : t("members.error.remove"));
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
        if (!orgId) throw new Error(t("members.error.noOrg"));
        await userServiceApi.invite(orgId, email, addRole);
      } else {
        await userServiceApi.addProjectMember(projectId, email, addRole);
      }
      setShowAdd(false);
      setEmail("");
      setAddRole("Developer");
      setSendInvite(false);
      await load();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : t("members.error.add"));
    } finally {
      setSubmitting(false);
    }
  }

  function handleProjectDeleted() {
    setDeleteTarget(null);
    refetchProjects();
    router.push("/projects");
  }

  if (!isAdmin(role)) {
    return (
      <div className="p-8">
        <p className="text-sm text-gray-500 dark:text-gray-400">{t("members.accessDenied")}</p>
      </div>
    );
  }

  const columns: Column<Member>[] = [
    {
      key: "member",
      header: t("members.col.member"),
      sortValue: (m) => m.display_name || m.email,
      render: (m) => (
        <div>
          <div className="font-medium text-gray-900 dark:text-gray-100">{m.display_name || m.email}</div>
          <div className="text-xs text-gray-500 dark:text-gray-400">{m.email}</div>
        </div>
      ),
    },
    {
      key: "type",
      header: t("members.col.type"),
      render: (m) => (
        <span className="text-xs text-gray-500 dark:text-gray-400">
          {m.principal_type === "service_account" ? t("members.type.serviceAccount") : t("members.type.user")}
        </span>
      ),
    },
    {
      key: "role",
      header: t("members.col.role"),
      sortValue: (m) => m.role,
      render: (m) => (
        <div className="flex items-center gap-2">
          <RolePill role={m.role} />
          <select
            aria-label={`Role for ${m.email}`}
            value={m.role}
            disabled={busyId === m.principal_id}
            onChange={(e) => changeRole(m, e.target.value as MemberRole)}
            className="rounded border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-1.5 py-0.5 text-xs"
          >
            {ROLES.map((r) => (
              <option key={r} value={r}>{t(`roles.${r}`)}</option>
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
          {t("common.remove")}
        </Button>
      ),
    },
  ];

  return (
    <div className="p-8">
      <Breadcrumb
        items={[
          { label: t("common.crumb.projects"), href: "/projects" },
          { label: project?.display_name ?? projectId, href: `/projects/${projectId}` },
          { label: t("nav.members") },
        ]}
      />

      <div className="mb-6 mt-2 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold text-gray-900 dark:text-gray-100">{t("members.title")}</h1>
          <p className="text-sm text-gray-500 dark:text-gray-400">{t("members.subtitle")}</p>
        </div>
        <Button onClick={() => setShowAdd(true)}>{t("members.modal.title")}</Button>
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
            searchPlaceholder={t("members.search.placeholder")}
            loading={loading}
            emptyState={<div className="py-12 text-center text-sm text-gray-500 dark:text-gray-400">{t("members.empty")}</div>}
          />
        </>
      )}

      <Modal isOpen={showAdd} onClose={() => setShowAdd(false)} title={t("members.modal.title")}>
        <form onSubmit={submitAdd} className="space-y-4">
          <div>
            <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-200">{t("members.modal.email.label")}</label>
            <input
              type="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder={t("members.modal.email.placeholder")}
              className="w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm"
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-200">{t("members.modal.role.label")}</label>
            <select
              value={addRole}
              onChange={(e) => setAddRole(e.target.value as MemberRole)}
              className="w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm"
            >
              {ROLES.map((r) => (
                <option key={r} value={r}>{t(`roles.${r}`)}</option>
              ))}
            </select>
          </div>
          <label className="flex items-start gap-2 text-sm text-gray-700 dark:text-gray-200">
            <input type="checkbox" checked={sendInvite} onChange={(e) => setSendInvite(e.target.checked)} className="mt-0.5" />
            <span>
              {t("members.modal.invite.label")}
              <span className="block text-xs text-gray-500 dark:text-gray-400">
                {t("members.modal.invite.help")}
                {sendInvite && !orgId && <span className="text-red-600">{t("members.modal.invite.noOrg")}</span>}
              </span>
            </span>
          </label>
          {actionError && <div className="text-sm text-red-600">{actionError}</div>}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={() => setShowAdd(false)}>{t("common.cancel")}</Button>
            <Button type="submit" isLoading={submitting} disabled={submitting || (sendInvite && !orgId)}>
              {sendInvite ? t("members.modal.submit.invite") : t("common.add")}
            </Button>
          </div>
        </form>
      </Modal>

      {loading && members.length === 0 && !error && (
        <div className="mt-4 flex justify-center"><Spinner /></div>
      )}

      <div className="mt-10 rounded-xl border border-red-200 dark:border-red-900 bg-white dark:bg-gray-900 px-5 py-5">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-sm font-semibold text-red-700 dark:text-red-400">{t("members.dangerZone.title")}</h2>
            <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("members.dangerZone.subtitle")}</p>
          </div>
          <button
            onClick={() => setDeleteTarget({ kind: "project", projectId, projectName: project?.name ?? projectId })}
            className="inline-flex items-center gap-2 rounded-lg border border-red-200 dark:border-red-900 bg-white dark:bg-gray-900 px-3 py-2 text-sm font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/30 transition-colors shadow-sm"
          >
            {t("members.dangerZone.delete")}
          </button>
        </div>
      </div>

      {deleteTarget && (
        <DeleteImpactModal
          key={deleteImpactTargetKey(deleteTarget)}
          target={deleteTarget}
          onClose={() => setDeleteTarget(null)}
          onDeleted={handleProjectDeleted}
        />
      )}
    </div>
  );
}

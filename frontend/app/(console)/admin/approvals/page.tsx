"use client";
import { useEffect, useState, FormEvent } from "react";
import { adminApi } from "@/lib/api";
import type { PendingApproval } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { DataTable, type Column } from "@/components/ui/data-table";
import { useProjectContext } from "@/lib/project-context";
import { canApprove } from "@/lib/rbac";
import { timeAgo } from "@/lib/format";
import { useT } from "@/lib/i18n/console/context";

function ResourcePill({ kind }: { kind: string }) {
  const tone = kind === "AIModel"
    ? "bg-indigo-50 text-indigo-700"
    : "bg-gray-100 text-gray-600";
  return (
    <span className={`inline-flex items-center rounded px-2 py-0.5 text-xs font-medium ${tone}`}>
      {kind}
    </span>
  );
}

function summarisePayload(action: string, payload: Record<string, unknown> | undefined): string {
  if (!payload) return "";
  if (action === "CreateAIModel") {
    const profile = payload.profile as string | undefined;
    const modelType = payload.model_type as string | undefined;
    const source = payload.source as string | undefined;
    const parts: string[] = [];
    if (modelType) parts.push(modelType);
    if (profile) parts.push(profile);
    if (source) parts.push(`from ${source}`);
    return parts.join(" · ");
  }
  return "";
}

export default function ApprovalsPage() {
  const { t } = useT();
  const { projects, projectsLoading } = useProjectContext();
  const isAdminAnywhere = projects.some((p) => canApprove(p.role));

  const [approvals, setApprovals] = useState<PendingApproval[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  const [rejectingOp, setRejectingOp] = useState<PendingApproval | null>(null);
  const [rejectReason, setRejectReason] = useState("");
  const [isSubmittingReject, setIsSubmittingReject] = useState(false);

  const [busyOpId, setBusyOpId] = useState<string | null>(null);

  async function load() {
    setActionError(null);
    try {
      const data = await adminApi.listApprovals();
      setApprovals(data.approvals ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("approvals.error.load"));
    } finally {
      setIsLoading(false);
    }
  }

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- fetch-on-mount; load() is the page's data source and there's no Suspense boundary above this client component.
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function approve(opId: string) {
    setBusyOpId(opId);
    setActionError(null);
    try {
      await adminApi.approve(opId);
      setApprovals((rows) => rows.filter((r) => r.operation.id !== opId));
    } catch (err) {
      setActionError(err instanceof Error ? err.message : t("approvals.error.approve"));
    } finally {
      setBusyOpId(null);
    }
  }

  async function submitReject(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!rejectingOp) return;
    setIsSubmittingReject(true);
    setActionError(null);
    try {
      await adminApi.reject(rejectingOp.operation.id, rejectReason);
      setApprovals((rows) => rows.filter((r) => r.operation.id !== rejectingOp.operation.id));
      setRejectingOp(null);
      setRejectReason("");
    } catch (err) {
      setActionError(err instanceof Error ? err.message : t("approvals.error.reject"));
    } finally {
      setIsSubmittingReject(false);
    }
  }

  const columns: Column<PendingApproval>[] = [
    {
      key: "project",
      header: t("approvals.col.project"),
      sortValue: (r) => r.project_name,
      render: (r) => <span className="font-mono text-gray-900">{r.project_name}</span>,
    },
    {
      key: "resource",
      header: t("approvals.col.resource"),
      render: (r) => (
        <div className="flex items-center gap-2">
          <ResourcePill kind={r.operation.resource_kind} />
          <span className="font-mono text-gray-700">{r.operation.resource_name}</span>
        </div>
      ),
    },
    { key: "action", header: t("approvals.col.action"), sortValue: (r) => r.operation.action, render: (r) => r.operation.action },
    { key: "by", header: t("approvals.col.requestedBy"), render: (r) => <span className="text-gray-600">{r.requested_by || "—"}</span> },
    {
      key: "age",
      header: t("approvals.col.age"),
      sortValue: (r) => new Date(r.operation.created_at).getTime(),
      render: (r) => <span className="text-xs text-gray-400">{timeAgo(r.operation.created_at)}</span>,
    },
    {
      key: "summary",
      header: t("approvals.col.summary"),
      render: (r) => <span className="text-xs text-gray-500">{summarisePayload(r.operation.action, r.operation.payload) || "—"}</span>,
    },
    {
      key: "decision",
      header: t("approvals.col.decision"),
      align: "right",
      render: (r) => {
        const busy = busyOpId === r.operation.id;
        return (
          <div className="flex items-center justify-end gap-2">
            <button
              onClick={() => approve(r.operation.id)}
              disabled={busy}
              className="inline-flex items-center gap-1 rounded-lg bg-green-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-green-700 disabled:opacity-50 transition-colors"
            >
              {busy ? <Spinner size="sm" /> : t("approvals.action.approve")}
            </button>
            <button
              onClick={() => { setRejectingOp(r); setRejectReason(""); }}
              disabled={busy}
              className="inline-flex items-center gap-1 rounded-lg border border-red-200 bg-white px-3 py-1.5 text-xs font-medium text-red-600 hover:bg-red-50 disabled:opacity-50 transition-colors"
            >
              {t("approvals.action.reject")}
            </button>
          </div>
        );
      },
    },
  ];

  if (!projectsLoading && !isAdminAnywhere) {
    return (
      <div>
        <Breadcrumb items={[
          { label: t("common.crumb.console"), href: "/projects" },
          { label: t("approvals.crumb.admin") },
          { label: t("approvals.crumb.approvals") },
        ]} />
        <div className="mt-4 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
          {t("approvals.accessDenied")}
        </div>
      </div>
    );
  }

  return (
    <div>
      <div className="mb-8 flex items-start justify-between">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.console"), href: "/projects" },
              { label: t("approvals.crumb.admin") },
              { label: t("approvals.crumb.approvals") },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900">{t("approvals.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500">
            {t("approvals.subtitle")}
          </p>
        </div>
        <button
          onClick={() => { setIsLoading(true); load(); }}
          className="inline-flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
        >
          <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
          {t("common.refresh")}
        </button>
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{error}</div>
      )}
      {actionError && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{actionError}</div>
      )}

      <DataTable<PendingApproval>
        loading={isLoading}
        rows={approvals}
        getRowKey={(r) => r.operation.id}
        searchText={(r) => `${r.project_name} ${r.operation.resource_name} ${r.operation.action} ${r.requested_by ?? ""}`}
        searchPlaceholder={t("approvals.search.placeholder")}
        pageSize={15}
        columns={columns}
        emptyState={
          <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-gray-50 py-16">
            <svg className="mb-3 h-12 w-12 text-gray-300" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
            </svg>
            <p className="text-sm font-medium text-gray-500">{t("approvals.empty.title")}</p>
            <p className="mt-1 text-xs text-gray-400">{t("approvals.empty.body")}</p>
          </div>
        }
      />

      <Modal
        isOpen={!!rejectingOp}
        onClose={() => { setRejectingOp(null); setRejectReason(""); }}
        title={t("approvals.reject.title")}
      >
        <form onSubmit={submitReject} className="space-y-4">
          <p className="text-sm text-gray-600">{t("approvals.reject.body")}</p>
          <div>
            <label className="block text-sm font-medium text-gray-700">{t("approvals.reject.reasonLabel")}</label>
            <textarea
              required
              value={rejectReason}
              onChange={(e) => setRejectReason(e.target.value)}
              rows={3}
              placeholder={t("approvals.reject.reasonPlaceholder")}
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-red-500 focus:outline-none focus:ring-1 focus:ring-red-500"
            />
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button" onClick={() => { setRejectingOp(null); setRejectReason(""); }}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100 transition-colors"
            >
              {t("common.cancel")}
            </button>
            <button
              type="submit" disabled={isSubmittingReject || !rejectReason.trim()}
              className="inline-flex items-center gap-2 rounded-lg bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {isSubmittingReject ? <><Spinner size="sm" /> {t("approvals.reject.submitting")}</> : t("approvals.action.reject")}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}

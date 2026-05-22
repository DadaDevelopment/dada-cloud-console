"use client";
import { useEffect, useState, FormEvent } from "react";
import Link from "next/link";
import { adminApi } from "@/lib/api";
import type { PendingApproval } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";

function timeAgo(dateStr: string): string {
  const diffSecs = Math.floor((Date.now() - new Date(dateStr).getTime()) / 1000);
  if (diffSecs < 60) return `${diffSecs}s ago`;
  const diffMins = Math.floor(diffSecs / 60);
  if (diffMins < 60) return `${diffMins}m ago`;
  const diffHours = Math.floor(diffMins / 60);
  if (diffHours < 24) return `${diffHours}h ago`;
  return `${Math.floor(diffHours / 24)}d ago`;
}

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
  const [approvals, setApprovals] = useState<PendingApproval[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  // Reject modal state
  const [rejectingOp, setRejectingOp] = useState<PendingApproval | null>(null);
  const [rejectReason, setRejectReason] = useState("");
  const [isSubmittingReject, setIsSubmittingReject] = useState(false);

  // Approve note state (lightweight inline)
  const [busyOpId, setBusyOpId] = useState<string | null>(null);

  async function load() {
    setActionError(null);
    try {
      const data = await adminApi.listApprovals();
      setApprovals(data.approvals ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load approvals");
    } finally {
      setIsLoading(false);
    }
  }

  useEffect(() => {
    load();
  }, []);

  async function approve(opId: string) {
    setBusyOpId(opId);
    setActionError(null);
    try {
      await adminApi.approve(opId);
      setApprovals((rows) => rows.filter((r) => r.operation.id !== opId));
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Failed to approve");
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
      setActionError(err instanceof Error ? err.message : "Failed to reject");
    } finally {
      setIsSubmittingReject(false);
    }
  }

  if (isLoading) {
    return <div className="flex h-64 items-center justify-center"><Spinner size="lg" /></div>;
  }

  return (
    <div>
      <div className="mb-8 flex items-start justify-between">
        <div>
          <div className="flex items-center gap-2 text-sm text-gray-500">
            <Link href="/projects" className="hover:text-gray-700">Console</Link>
            <span>/</span>
            <span className="text-gray-900">Admin</span>
            <span>/</span>
            <span className="text-gray-900">Approvals</span>
          </div>
          <h1 className="mt-2 text-2xl font-bold text-gray-900">Pending approvals</h1>
          <p className="mt-0.5 text-sm text-gray-500">
            Operations parked in <span className="font-mono">WaitingForApproval</span>. First consumer is the AI Studio GPU gate.
          </p>
        </div>
        <button
          onClick={() => { setIsLoading(true); load(); }}
          className="inline-flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
        >
          <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
          Refresh
        </button>
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{error}</div>
      )}
      {actionError && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{actionError}</div>
      )}

      {approvals.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-gray-50 py-16">
          <svg className="mb-3 h-12 w-12 text-gray-300" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
          </svg>
          <p className="text-sm font-medium text-gray-500">No pending approvals</p>
          <p className="mt-1 text-xs text-gray-400">GPU model requests and other privileged operations will appear here.</p>
        </div>
      ) : (
        <div className="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm">
          <table className="min-w-full divide-y divide-gray-200">
            <thead className="bg-gray-50">
              <tr>
                <Th>Project</Th>
                <Th>Resource</Th>
                <Th>Action</Th>
                <Th>Requested by</Th>
                <Th>Age</Th>
                <Th>Summary</Th>
                <Th right>Decision</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {approvals.map((row) => {
                const op = row.operation;
                const summary = summarisePayload(op.action, op.payload);
                const busy = busyOpId === op.id;
                return (
                  <tr key={op.id} className="hover:bg-gray-50">
                    <td className="px-5 py-3 font-mono text-sm text-gray-900">{row.project_name}</td>
                    <td className="px-5 py-3 text-sm">
                      <div className="flex items-center gap-2">
                        <ResourcePill kind={op.resource_kind} />
                        <span className="font-mono text-gray-700">{op.resource_name}</span>
                      </div>
                    </td>
                    <td className="px-5 py-3 text-sm text-gray-700">{op.action}</td>
                    <td className="px-5 py-3 text-sm text-gray-600">{row.requested_by || "—"}</td>
                    <td className="px-5 py-3 text-xs text-gray-400">{timeAgo(op.created_at)}</td>
                    <td className="px-5 py-3 text-xs text-gray-500">{summary || "—"}</td>
                    <td className="px-5 py-3">
                      <div className="flex items-center justify-end gap-2">
                        <button
                          onClick={() => approve(op.id)}
                          disabled={busy}
                          className="inline-flex items-center gap-1 rounded-lg bg-green-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-green-700 disabled:opacity-50 transition-colors"
                        >
                          {busy ? <Spinner size="sm" /> : "Approve"}
                        </button>
                        <button
                          onClick={() => { setRejectingOp(row); setRejectReason(""); }}
                          disabled={busy}
                          className="inline-flex items-center gap-1 rounded-lg border border-red-200 bg-white px-3 py-1.5 text-xs font-medium text-red-600 hover:bg-red-50 disabled:opacity-50 transition-colors"
                        >
                          Reject
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      <Modal
        isOpen={!!rejectingOp}
        onClose={() => { setRejectingOp(null); setRejectReason(""); }}
        title="Reject operation"
      >
        <form onSubmit={submitReject} className="space-y-4">
          <p className="text-sm text-gray-600">
            The reason will be stored on the operation and shown to the requester in the operations timeline.
          </p>
          <div>
            <label className="block text-sm font-medium text-gray-700">Reason</label>
            <textarea
              required
              value={rejectReason}
              onChange={(e) => setRejectReason(e.target.value)}
              rows={3}
              placeholder="GPU capacity reserved for prod migration this week. Try again Monday."
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-red-500 focus:outline-none focus:ring-1 focus:ring-red-500"
            />
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button" onClick={() => { setRejectingOp(null); setRejectReason(""); }}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100 transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit" disabled={isSubmittingReject || !rejectReason.trim()}
              className="inline-flex items-center gap-2 rounded-lg bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {isSubmittingReject ? <><Spinner size="sm" /> Rejecting...</> : "Reject"}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}

function Th({ children, right }: { children: React.ReactNode; right?: boolean }) {
  return (
    <th className={`px-5 py-3 text-xs font-semibold uppercase tracking-wide text-gray-500 ${right ? "text-right" : "text-left"}`}>
      {children}
    </th>
  );
}

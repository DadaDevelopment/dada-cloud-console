"use client";
import { useEffect, useState } from "react";
import { useParams, useSearchParams } from "next/navigation";
import { projectsApi } from "@/lib/api";
import type { Operation, OperationStatus } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { timeAgo } from "@/lib/format";
import { useT } from "@/lib/i18n/console/context";

const IN_PROGRESS_STATUSES = new Set<OperationStatus>([
  "Created", "Validated", "Queued", "Rendering",
  "CommittingToGit", "Committed", "WaitingForArgoSync",
  "Syncing", "Reconciling", "WaitingForApproval",
]);

function isInProgress(status: OperationStatus): boolean {
  return IN_PROGRESS_STATUSES.has(status);
}

function statusColor(status: OperationStatus): string {
  if (status === "Ready") return "bg-green-100 dark:bg-green-950/40 text-green-700 dark:text-green-300";
  if (status === "Failed") return "bg-red-100 dark:bg-red-950/40 text-red-700 dark:text-red-300";
  if (status === "Cancelled") return "bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-400";
  if (status === "WaitingForApproval") return "bg-yellow-100 dark:bg-yellow-950/40 text-yellow-700 dark:text-yellow-300";
  return "bg-blue-100 dark:bg-blue-950/40 text-blue-700 dark:text-blue-300";
}

function statusDot(status: OperationStatus): string {
  if (status === "Ready") return "bg-green-500";
  if (status === "Failed") return "bg-red-500";
  if (status === "Cancelled") return "bg-gray-400";
  if (status === "WaitingForApproval") return "bg-yellow-500";
  return "bg-blue-500";
}

function StatusIcon({ status }: { status: OperationStatus }) {
  if (status === "Ready") {
    return (
      <svg className="h-4 w-4 text-green-600" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
      </svg>
    );
  }
  if (status === "Failed") {
    return (
      <svg className="h-4 w-4 text-red-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
      </svg>
    );
  }
  if (status === "Cancelled") {
    return (
      <svg className="h-4 w-4 text-gray-400 dark:text-gray-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636" />
      </svg>
    );
  }
  if (status === "WaitingForApproval") {
    return (
      <svg className="h-4 w-4 text-yellow-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
    );
  }
  return (
    <svg className="h-4 w-4 animate-spin text-blue-500" viewBox="0 0 24 24" fill="none">
      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
    </svg>
  );
}

export default function OperationsPage() {
  const params = useParams<{ projectId: string }>();
  const searchParams = useSearchParams();
  const projectId = params.projectId;
  const highlightId = searchParams.get("highlight");
  const { t } = useT();

  const [operations, setOperations] = useState<Operation[]>([]);
  const [loadedProjectId, setLoadedProjectId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [expandedId, setExpandedId] = useState<string | null>(highlightId);
  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState<"all" | "in-progress" | "Ready" | "Failed" | "WaitingForApproval">("all");

  useEffect(() => {
    let cancelled = false;
    void projectsApi
      .operations(projectId)
      .then((data) => {
        if (cancelled) return;
        setOperations(data.operations ?? []);
        setError(null);
      })
      .catch((err) => {
        if (cancelled) return;
        setError(err instanceof Error ? err.message : t("operations.error.load"));
      })
      .finally(() => {
        if (!cancelled) {
          setLoadedProjectId(projectId);
        }
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId]);

  useEffect(() => {
    const hasInProgress = operations.some((op) => isInProgress(op.status));
    if (!hasInProgress) return;
    const interval = setInterval(() => {
      void projectsApi.operations(projectId).then((data) => {
        setOperations(data.operations ?? []);
        setError(null);
      });
    }, 3000);
    return () => clearInterval(interval);
  }, [operations, projectId]);

  if (loadedProjectId !== projectId) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner size="lg" />
      </div>
    );
  }

  const hasInProgress = operations.some((op) => isInProgress(op.status));

  const q = query.trim().toLowerCase();
  const filtered = operations.filter((op) => {
    if (statusFilter === "in-progress" && !isInProgress(op.status)) return false;
    if (statusFilter !== "all" && statusFilter !== "in-progress" && op.status !== statusFilter) return false;
    if (!q) return true;
    return (
      op.action.toLowerCase().includes(q) ||
      op.resource_name.toLowerCase().includes(q) ||
      (op.resource_kind ?? "").toLowerCase().includes(q)
    );
  });

  return (
    <div>
      <div className="mb-8">
        <Breadcrumb
          items={[
            { label: t("common.crumb.projects"), href: "/projects" },
            { label: t("common.crumb.overview"), href: `/projects/${projectId}` },
            { label: t("nav.operations") },
          ]}
        />
        <div className="mt-2 flex items-center gap-3">
          <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">{t("operations.title")}</h1>
          {hasInProgress && (
            <span className="inline-flex items-center gap-1.5 rounded-full bg-blue-50 dark:bg-blue-950/40 px-2.5 py-1 text-xs font-medium text-blue-600 dark:text-blue-400">
              <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-blue-600" />
              {t("operations.live")}
            </span>
          )}
        </div>
        <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("operations.subtitle")}</p>
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          {error}
        </div>
      )}

      {operations.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 py-16">
          <svg className="mb-3 h-12 w-12 text-gray-300 dark:text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2" />
          </svg>
          <p className="text-sm font-medium text-gray-500 dark:text-gray-400">{t("operations.empty.title")}</p>
          <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("operations.empty.subtitle")}</p>
        </div>
      ) : (
        <>
          <div className="mb-4 flex flex-wrap items-center gap-3">
            <div className="relative flex-1 min-w-[16rem]">
              <svg className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400 dark:text-gray-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z" />
              </svg>
              <input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder={t("operations.search.placeholder")}
                aria-label={t("operations.search.placeholder")}
                className="w-full rounded-lg border border-gray-300 dark:border-gray-700 py-2 pl-9 pr-3 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <select
              value={statusFilter}
              onChange={(e) => setStatusFilter(e.target.value as typeof statusFilter)}
              aria-label={t("common.status.status")}
              className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="all">{t("operations.filter.all")}</option>
              <option value="in-progress">{t("operations.filter.inProgress")}</option>
              <option value="Ready">{t("operations.filter.ready")}</option>
              <option value="Failed">{t("operations.filter.failed")}</option>
              <option value="WaitingForApproval">{t("operations.filter.waitingForApproval")}</option>
            </select>
            <span className="text-xs text-gray-400 dark:text-gray-500">
              {t("operations.countOf", { count: filtered.length, total: operations.length })}
            </span>
          </div>

          {filtered.length === 0 ? (
            <div className="rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 py-12 text-center text-sm text-gray-400 dark:text-gray-500">
              {t("operations.noMatch")}
            </div>
          ) : (
          <div className="overflow-hidden rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900">
            {filtered.map((op, idx) => {
              const isExpanded = expandedId === op.id;
              const isHighlighted = highlightId === op.id;
              return (
              <div
                key={op.id}
                className={`${idx < filtered.length - 1 ? "border-b border-gray-100 dark:border-gray-800" : ""} ${
                  isHighlighted ? "bg-blue-50/50" : ""
                }`}
              >
                <button
                  onClick={() => setExpandedId(isExpanded ? null : op.id)}
                  className="w-full px-5 py-4 text-left hover:bg-gray-50 dark:hover:bg-gray-800 transition-colors"
                >
                  <div className="flex items-center gap-4">
                    <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full border border-gray-100 dark:border-gray-800 bg-white dark:bg-gray-900 shadow-sm">
                      <StatusIcon status={op.status} />
                    </div>

                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="text-sm font-medium text-gray-900 dark:text-gray-100">{op.action}</span>
                        <span className="font-mono text-xs text-gray-500 dark:text-gray-400">{op.resource_name}</span>
                        {op.resource_kind && (
                          <span className="rounded bg-gray-100 dark:bg-gray-800 px-1.5 py-0.5 font-mono text-xs text-gray-500 dark:text-gray-400">
                            {op.resource_kind}
                          </span>
                        )}
                      </div>
                      <div className="mt-1 flex items-center gap-3 text-xs text-gray-400 dark:text-gray-500">
                        <span>{timeAgo(op.created_at)}</span>
                        {op.git_commit && (
                          <>
                            <span>·</span>
                            <span className="font-mono">{op.git_commit.slice(0, 8)}</span>
                          </>
                        )}
                      </div>
                    </div>

                    <div className="flex items-center gap-2">
                      <span className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium ${statusColor(op.status)}`}>
                        <span className={`h-1.5 w-1.5 rounded-full ${statusDot(op.status)}`} />
                        {op.status}
                      </span>
                      <svg
                        className={`h-4 w-4 text-gray-400 dark:text-gray-500 transition-transform ${isExpanded ? "rotate-180" : ""}`}
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                      >
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
                      </svg>
                    </div>
                  </div>
                </button>

                {isExpanded && (
                  <div className="border-t border-gray-100 dark:border-gray-800 bg-gray-50 dark:bg-gray-900 px-5 py-4">
                    <dl className="grid grid-cols-2 gap-x-6 gap-y-3 text-sm sm:grid-cols-3">
                      <div>
                        <dt className="text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("operations.detail.operationId")}</dt>
                        <dd className="mt-1 font-mono text-xs text-gray-700 dark:text-gray-200">{op.id}</dd>
                      </div>
                      <div>
                        <dt className="text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("operations.detail.action")}</dt>
                        <dd className="mt-1 text-xs text-gray-700 dark:text-gray-200">{op.action}</dd>
                      </div>
                      <div>
                        <dt className="text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("operations.detail.resource")}</dt>
                        <dd className="mt-1 font-mono text-xs text-gray-700 dark:text-gray-200">{op.resource_name}</dd>
                      </div>
                      {op.git_commit && (
                        <div>
                          <dt className="text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("operations.detail.gitCommit")}</dt>
                          <dd className="mt-1 font-mono text-xs text-gray-700 dark:text-gray-200">{op.git_commit}</dd>
                        </div>
                      )}
                      {op.git_path && (
                        <div>
                          <dt className="text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("operations.detail.gitPath")}</dt>
                          <dd className="mt-1 font-mono text-xs text-gray-700 dark:text-gray-200 break-all">{op.git_path}</dd>
                        </div>
                      )}
                      <div>
                        <dt className="text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("operations.detail.created")}</dt>
                        <dd className="mt-1 text-xs text-gray-700 dark:text-gray-200">{new Date(op.created_at).toLocaleString()}</dd>
                      </div>
                      <div>
                        <dt className="text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("operations.detail.updated")}</dt>
                        <dd className="mt-1 text-xs text-gray-700 dark:text-gray-200">{new Date(op.updated_at).toLocaleString()}</dd>
                      </div>
                    </dl>
                    {op.error_message && (
                      <div className="mt-3 rounded-lg border border-red-200 bg-red-50 px-3 py-2">
                        <p className="text-xs font-medium text-red-700">{t("operations.detail.error")}</p>
                        <p className="mt-0.5 font-mono text-xs text-red-600">{op.error_message}</p>
                      </div>
                    )}
                  </div>
                )}
              </div>
              );
            })}
          </div>
          )}
        </>
      )}
    </div>
  );
}

"use client";
import { useEffect, useState, FormEvent } from "react";
import { useParams } from "next/navigation";
import { customDomainsApi } from "@/lib/api";
import type { DomainAuthorization, DomainChallenge } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { timeAgo } from "@/lib/format";
import { EmptyState } from "@/components/ui/empty-state";
import { StateChip } from "@/components/ui/state-chip";
import type { ChipTone } from "@/components/ui/state-chip";
import { useT } from "@/lib/i18n/console/context";

function domainStatusTone(status: DomainAuthorization["status"]): ChipTone {
  switch (status) {
    case "verified": return "ready";
    case "failed": return "error";
    default: return "needs-action";
  }
}

function CopyField({ label, value }: { label: string; value: string }) {
  const { t } = useT();
  const [copied, setCopied] = useState(false);
  return (
    <div>
      <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{label}</p>
      <div className="mt-1 flex items-center gap-2">
        <code className="flex-1 break-all rounded-md bg-gray-50 dark:bg-gray-900 border border-gray-200 dark:border-gray-800 px-3 py-2 text-xs text-gray-800 dark:text-gray-200">
          {value}
        </code>
        <button
          type="button"
          onClick={() => {
            navigator.clipboard.writeText(value).then(() => {
              setCopied(true);
              setTimeout(() => setCopied(false), 1500);
            });
          }}
          className="rounded-md border border-gray-300 dark:border-gray-700 px-2 py-2 text-xs text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
        >
          {copied ? t("common.copied") : t("common.copy")}
        </button>
      </div>
    </div>
  );
}

function ChallengeBlock({ challenge }: { challenge: DomainChallenge }) {
  const { t } = useT();
  return (
    <div className="mt-3 space-y-3 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4">
      <p className="text-sm text-gray-700 dark:text-gray-200">
        {t("domains.challenge.instruction", { type: challenge.type })}
      </p>
      <CopyField label={t("domains.challenge.fieldType")} value={challenge.type} />
      <CopyField label={t("domains.challenge.fieldHost")} value={challenge.host} />
      <CopyField label={t("domains.challenge.fieldValue")} value={challenge.value} />
    </div>
  );
}

export default function ProjectDomainsPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const { project, role } = useProjectContext();
  const { t } = useT();

  const [auths, setAuths] = useState<DomainAuthorization[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isModalOpen, setIsModalOpen] = useState(false);
  const [apexInput, setApexInput] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  const [verifyingId, setVerifyingId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);

  const canEdit = canMutate(role);

  useEffect(() => {
    /* eslint-disable react-hooks/set-state-in-effect */
    setIsLoading(true);
    setError(null);
    /* eslint-enable react-hooks/set-state-in-effect */
    customDomainsApi
      .listAuthorizations(projectId)
      .then((data) => setAuths(data.authorizations ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : t("domains.error.load")))
      .finally(() => setIsLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId]);

  async function handleAdd(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitError(null);
    setIsSubmitting(true);
    try {
      const result = await customDomainsApi.addAuthorization(projectId, apexInput.trim());
      const created = { ...result.authorization, challenge: result.challenge };
      setAuths((prev) => [created, ...prev.filter((a) => a.id !== created.id)]);
      setApexInput("");
      setIsModalOpen(false);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : t("domains.error.add"));
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleVerify(id: string) {
    setVerifyingId(id);
    setError(null);
    try {
      const result = await customDomainsApi.verifyAuthorization(projectId, id);
      const updated = { ...result.authorization, challenge: result.challenge };
      setAuths((prev) => prev.map((a) => (a.id === id ? updated : a)));
    } catch (err) {
      setError(err instanceof Error ? err.message : t("domains.error.verify"));
    } finally {
      setVerifyingId(null);
    }
  }

  async function handleDelete(id: string) {
    if (!confirm(t("domains.confirm.remove"))) return;
    setDeletingId(id);
    setError(null);
    try {
      await customDomainsApi.deleteAuthorization(projectId, id);
      setAuths((prev) => prev.filter((a) => a.id !== id));
    } catch (err) {
      setError(err instanceof Error ? err.message : t("domains.error.delete"));
    } finally {
      setDeletingId(null);
    }
  }

  function rowTimestamp(a: DomainAuthorization): string {
    if (a.status === "verified" && a.verified_at) {
      return t("domains.row.verifiedAt", { ago: timeAgo(a.verified_at) });
    }
    if (a.last_checked_at) {
      return t("domains.row.lastChecked", { ago: timeAgo(a.last_checked_at) });
    }
    return t("domains.row.added", { ago: timeAgo(a.created_at) });
  }

  function domainStatusLabel(status: DomainAuthorization["status"]): string {
    switch (status) {
      case "verified": return t("domains.status.verified");
      case "failed": return t("domains.status.failed");
      default: return t("domains.status.pending");
    }
  }

  return (
    <div>
      <div className="mb-8 flex flex-wrap items-start justify-between gap-3">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.projects"), href: "/projects" },
              { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
              { label: t("nav.domains") },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("domains.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("domains.subtitle")}</p>
        </div>
        {canEdit && (
          <button
            onClick={() => {
              setApexInput("");
              setSubmitError(null);
              setIsModalOpen(true);
            }}
            className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 transition-colors"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            {t("domains.add")}
          </button>
        )}
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
          {error}
        </div>
      )}

      {isLoading ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : auths.length === 0 ? (
        <div className="space-y-4">
          <EmptyState
            title={t("domains.empty.title")}
            description={t("domains.empty.description")}
          />
          {canEdit && (
            <div className="flex justify-center">
              <button
                onClick={() => setIsModalOpen(true)}
                className="text-sm font-medium text-blue-600 hover:text-blue-700"
              >
                {t("domains.empty.cta")}
              </button>
            </div>
          )}
        </div>
      ) : (
        <div className="space-y-4">
          {auths.map((a) => (
            <div key={a.id} className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
              <div className="flex items-start justify-between">
                <div>
                  <div className="flex items-center gap-3">
                    <p className="font-mono text-base font-semibold text-gray-900 dark:text-gray-100">{a.apex_domain}</p>
                    <StateChip tone={domainStatusTone(a.status)} dot>
                      {domainStatusLabel(a.status)}
                    </StateChip>
                  </div>
                  <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">
                    {rowTimestamp(a)}
                    {a.error_message ? ` · ${a.error_message}` : ""}
                  </p>
                </div>
                {canEdit && (
                  <div className="flex items-center gap-2">
                    {a.status !== "verified" && (
                      <button
                        onClick={() => handleVerify(a.id)}
                        disabled={verifyingId === a.id}
                        className="inline-flex items-center gap-2 rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-100 dark:hover:bg-gray-800 disabled:opacity-50 transition-colors"
                      >
                        {verifyingId === a.id ? <Spinner size="sm" /> : null}
                        {t("domains.action.verify")}
                      </button>
                    )}
                    <button
                      onClick={() => handleDelete(a.id)}
                      disabled={deletingId === a.id}
                      className="rounded-lg border border-red-200 dark:border-red-900 px-3 py-1.5 text-sm font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/40 disabled:opacity-50 transition-colors"
                    >
                      {deletingId === a.id ? t("common.removing") : t("common.remove")}
                    </button>
                  </div>
                )}
              </div>

              {a.status !== "verified" && a.challenge && <ChallengeBlock challenge={a.challenge} />}
            </div>
          ))}
        </div>
      )}

      <Modal
        isOpen={isModalOpen}
        onClose={() => {
          setIsModalOpen(false);
          setSubmitError(null);
        }}
        title={t("domains.modal.title")}
      >
        <form onSubmit={handleAdd} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("domains.modal.apexLabel")}</label>
            <input
              type="text"
              required
              value={apexInput}
              onChange={(e) => setApexInput(e.target.value)}
              placeholder="acme.com"
              pattern="[A-Za-z0-9.\-]+"
              title={t("domains.modal.apexTitle")}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">
              {t("domains.modal.apexHelp")}
            </p>
          </div>

          {submitError && (
            <div role="alert" className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
              {submitError}
            </div>
          )}

          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => {
                setIsModalOpen(false);
                setSubmitError(null);
              }}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
            >
              {t("common.cancel")}
            </button>
            <button
              type="submit"
              disabled={isSubmitting}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {isSubmitting ? (
                <>
                  <Spinner size="sm" />
                  {t("domains.modal.adding")}
                </>
              ) : (
                t("domains.add")
              )}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}

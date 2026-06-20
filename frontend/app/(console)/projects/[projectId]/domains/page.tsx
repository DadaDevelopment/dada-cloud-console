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

// Level 1 of the Vercel-style model: prove ownership of an apex domain.
// Once an apex is "verified", that project may attach the apex + any of its
// subdomains to apps (Level 2 lives on the app settings "Domains" tab).

function statusStyle(status: DomainAuthorization["status"]): string {
  switch (status) {
    case "verified":
      return "bg-green-50 text-green-700 border-green-200";
    case "failed":
      return "bg-red-50 text-red-700 border-red-200";
    default:
      return "bg-amber-50 text-amber-700 border-amber-200";
  }
}

function CopyField({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div>
      <p className="text-xs font-medium text-gray-500">{label}</p>
      <div className="mt-1 flex items-center gap-2">
        <code className="flex-1 break-all rounded-md bg-gray-50 border border-gray-200 px-3 py-2 text-xs text-gray-800">
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
          className="rounded-md border border-gray-300 px-2 py-2 text-xs text-gray-600 hover:bg-gray-100 transition-colors"
        >
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
    </div>
  );
}

function ChallengeBlock({ challenge }: { challenge: DomainChallenge }) {
  return (
    <div className="mt-3 space-y-3 rounded-lg border border-gray-200 bg-white p-4">
      <p className="text-sm text-gray-700">
        Add this <span className="font-semibold">{challenge.type}</span> record at your DNS provider,
        then click <span className="font-semibold">Verify</span>. Verification can take a few minutes
        to propagate.
      </p>
      <CopyField label="Type" value={challenge.type} />
      <CopyField label="Host / Name" value={challenge.host} />
      <CopyField label="Value" value={challenge.value} />
    </div>
  );
}

export default function ProjectDomainsPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const { project, role } = useProjectContext();

  const [auths, setAuths] = useState<DomainAuthorization[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isModalOpen, setIsModalOpen] = useState(false);
  const [apexInput, setApexInput] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  // Per-row transient state.
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
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load domains"))
      .finally(() => setIsLoading(false));
  }, [projectId]);

  async function handleAdd(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitError(null);
    setIsSubmitting(true);
    try {
      const result = await customDomainsApi.addAuthorization(projectId, apexInput.trim());
      // Splice in the challenge so the new row shows the TXT record immediately.
      const created = { ...result.authorization, challenge: result.challenge };
      setAuths((prev) => [created, ...prev.filter((a) => a.id !== created.id)]);
      setApexInput("");
      setIsModalOpen(false);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : "Failed to add domain");
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
      setError(err instanceof Error ? err.message : "Verification failed");
    } finally {
      setVerifyingId(null);
    }
  }

  async function handleDelete(id: string) {
    if (!confirm("Remove this domain authorization? Attached hostnames must be detached first.")) return;
    setDeletingId(id);
    setError(null);
    try {
      await customDomainsApi.deleteAuthorization(projectId, id);
      setAuths((prev) => prev.filter((a) => a.id !== id));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete domain");
    } finally {
      setDeletingId(null);
    }
  }

  return (
    <div>
      {/* Header */}
      <div className="mb-8 flex items-start justify-between">
        <div>
          <Breadcrumb
            items={[
              { label: "Projects", href: "/projects" },
              { label: project?.display_name ?? "Overview", href: `/projects/${projectId}` },
              { label: "Domains" },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900">Domains</h1>
          <p className="mt-0.5 text-sm text-gray-500">
            Authorize apex domains you own. A verified apex lets you attach it and any of its
            subdomains to apps, with automatic TLS.
          </p>
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
            Add Domain
          </button>
        )}
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          {error}
        </div>
      )}

      {isLoading ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : auths.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-gray-50 py-16">
          <svg className="mb-3 h-12 w-12 text-gray-300" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M21 12a9 9 0 11-18 0 9 9 0 0118 0zM3.6 9h16.8M3.6 15h16.8M12 3a15 15 0 010 18M12 3a15 15 0 000 18" />
          </svg>
          <p className="text-sm font-medium text-gray-500">No authorized domains yet</p>
          {canEdit && (
            <button
              onClick={() => setIsModalOpen(true)}
              className="mt-4 text-sm text-blue-600 hover:text-blue-700"
            >
              Authorize your first domain →
            </button>
          )}
        </div>
      ) : (
        <div className="space-y-4">
          {auths.map((a) => (
            <div key={a.id} className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
              <div className="flex items-start justify-between">
                <div>
                  <div className="flex items-center gap-3">
                    <p className="font-mono text-base font-semibold text-gray-900">{a.apex_domain}</p>
                    <span className={`rounded-full border px-2 py-0.5 text-xs font-medium capitalize ${statusStyle(a.status)}`}>
                      {a.status}
                    </span>
                  </div>
                  <p className="mt-1 text-xs text-gray-400">
                    {a.status === "verified" && a.verified_at
                      ? `Verified ${timeAgo(a.verified_at)}`
                      : a.last_checked_at
                        ? `Last checked ${timeAgo(a.last_checked_at)}`
                        : `Added ${timeAgo(a.created_at)}`}
                    {a.error_message ? ` · ${a.error_message}` : ""}
                  </p>
                </div>
                {canEdit && (
                  <div className="flex items-center gap-2">
                    {a.status !== "verified" && (
                      <button
                        onClick={() => handleVerify(a.id)}
                        disabled={verifyingId === a.id}
                        className="inline-flex items-center gap-2 rounded-lg border border-gray-300 px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-100 disabled:opacity-50 transition-colors"
                      >
                        {verifyingId === a.id ? <Spinner size="sm" /> : null}
                        Verify
                      </button>
                    )}
                    <button
                      onClick={() => handleDelete(a.id)}
                      disabled={deletingId === a.id}
                      className="rounded-lg border border-red-200 px-3 py-1.5 text-sm font-medium text-red-600 hover:bg-red-50 disabled:opacity-50 transition-colors"
                    >
                      {deletingId === a.id ? "Removing…" : "Remove"}
                    </button>
                  </div>
                )}
              </div>

              {/* Show the TXT challenge until verified. */}
              {a.status !== "verified" && a.challenge && <ChallengeBlock challenge={a.challenge} />}
            </div>
          ))}
        </div>
      )}

      {/* Add domain modal */}
      <Modal
        isOpen={isModalOpen}
        onClose={() => {
          setIsModalOpen(false);
          setSubmitError(null);
        }}
        title="Authorize a Domain"
      >
        <form onSubmit={handleAdd} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700">Apex Domain</label>
            <input
              type="text"
              required
              value={apexInput}
              onChange={(e) => setApexInput(e.target.value)}
              placeholder="acme.com"
              pattern="[A-Za-z0-9.\-]+"
              title="A bare domain like acme.com (no http://, no subdomain, no path)"
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <p className="mt-1 text-xs text-gray-400">
              Enter the apex (root) domain you own, e.g. <code>acme.com</code>. Verifying it
              authorizes the apex <span className="font-medium">and all subdomains</span> for this
              project.
            </p>
          </div>

          {submitError && (
            <div role="alert" className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
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
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100 transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={isSubmitting}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {isSubmitting ? (
                <>
                  <Spinner size="sm" />
                  Adding…
                </>
              ) : (
                "Add Domain"
              )}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}

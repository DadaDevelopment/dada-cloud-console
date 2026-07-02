"use client";
import { useEffect, useState, FormEvent } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { customDomainsApi } from "@/lib/api";
import type { DomainHostname } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { PhaseBadge } from "@/components/ui/phase-badge";

// Level 2 of the Vercel-style model: attach a hostname (apex or subdomain under
// an already-verified apex authorization) to this specific app + environment.
// Ownership/anti-hijack is enforced server-side — the apex must be verified for
// THIS project before any of its hostnames can be attached here.

function statusStyle(status: DomainHostname["status"]): string {
  switch (status) {
    case "active":
      return "bg-green-50 text-green-700 border-green-200";
    case "failed":
      return "bg-red-50 text-red-700 border-red-200";
    default:
      return "bg-amber-50 text-amber-700 border-amber-200";
  }
}

interface Props {
  projectId: string;
  envId: string;
  appName: string;
  canEdit: boolean;
}

export function HostnamesManager({ projectId, envId, appName, canEdit }: Props) {
  const router = useRouter();
  const [hostnames, setHostnames] = useState<DomainHostname[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [input, setInput] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [dnsHint, setDnsHint] = useState<{ type: string; host: string; target: string } | null>(null);
  const [detachingId, setDetachingId] = useState<string | null>(null);

  useEffect(() => {
    /* eslint-disable react-hooks/set-state-in-effect */
    if (!envId) {
      setIsLoading(false);
      return;
    }
    setIsLoading(true);
    setError(null);
    /* eslint-enable react-hooks/set-state-in-effect */
    customDomainsApi
      .listHostnames(projectId, envId, appName)
      .then((data) => setHostnames(data.hostnames ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load hostnames"))
      .finally(() => setIsLoading(false));
  }, [projectId, envId, appName]);

  async function handleAttach(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitError(null);
    setDnsHint(null);
    setIsSubmitting(true);
    try {
      const result = await customDomainsApi.attachHostname(projectId, envId, appName, input.trim());
      setHostnames((prev) => [result.hostname, ...prev.filter((h) => h.id !== result.hostname.id)]);
      setDnsHint(result.dns_record);
      setInput("");
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : "Failed to attach hostname");
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleDetach(h: DomainHostname) {
    if (!confirm(`Detach ${h.hostname}? Its TLS certificate and ingress will be removed.`)) return;
    setDetachingId(h.id);
    setError(null);
    try {
      const result = await customDomainsApi.detachHostname(projectId, envId, appName, h.id);
      setHostnames((prev) => prev.filter((x) => x.id !== h.id));
      const opId = result.operation?.id;
      if (opId) router.push(`/projects/${projectId}/operations?highlight=${opId}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to detach hostname");
      setDetachingId(null);
    }
  }

  if (isLoading) {
    return (
      <div className="flex h-32 items-center justify-center">
        <Spinner />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="rounded-xl border border-gray-200 bg-white px-5 py-6">
        <h2 className="text-lg font-semibold text-gray-900">Custom hostnames</h2>
        <p className="mt-1 text-sm text-gray-500">
          Attach a hostname under a domain you&apos;ve verified for this project. TLS is issued
          automatically. Authorize apex domains on the{" "}
          <Link href={`/projects/${projectId}/domains`} className="font-medium text-blue-600 hover:text-blue-700">
            project Domains
          </Link>{" "}
          page first.
        </p>

        {canEdit && (
          <form onSubmit={handleAttach} className="mt-4 flex flex-wrap items-start gap-3">
            <input
              type="text"
              required
              value={input}
              onChange={(e) => setInput(e.target.value)}
              placeholder="shop.acme.com"
              pattern="[A-Za-z0-9.\-]+"
              title="A hostname under a verified apex, e.g. shop.acme.com or acme.com"
              className="min-w-64 flex-1 rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <button
              type="submit"
              disabled={isSubmitting || !envId}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {isSubmitting ? <Spinner size="sm" /> : null}
              Attach
            </button>
          </form>
        )}

        {submitError && (
          <div role="alert" className="mt-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
            {submitError}
          </div>
        )}

        {dnsHint && (
          <div className="mt-3 rounded-lg border border-blue-200 bg-blue-50 px-4 py-3 text-sm text-blue-800">
            <p className="font-medium">Point your DNS at the platform:</p>
            <p className="mt-1 font-mono text-xs">
              {dnsHint.type} {dnsHint.host} → {dnsHint.target}
            </p>
            <p className="mt-1 text-xs text-blue-700">
              The certificate is issued once DNS resolves to the platform ingress.
            </p>
          </div>
        )}
      </div>

      {error && (
        <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          {error}
        </div>
      )}

      {hostnames.length === 0 ? (
        <div className="rounded-lg border border-dashed border-gray-300 bg-gray-50 px-5 py-8 text-center text-sm text-gray-500">
          No custom hostnames attached to this app.
        </div>
      ) : (
        <div className="overflow-x-auto rounded-xl border border-gray-200 bg-white">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-left text-xs font-medium text-gray-500">
              <tr>
                <th className="px-5 py-3">Hostname</th>
                <th className="px-5 py-3">Record</th>
                <th className="px-5 py-3">Status</th>
                <th className="px-5 py-3">Certificate</th>
                {canEdit && <th className="px-5 py-3" />}
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {hostnames.map((h) => (
                <tr key={h.id}>
                  <td className="px-5 py-3 font-mono text-gray-900">{h.hostname}</td>
                  <td className="px-5 py-3 text-gray-500">{h.record_type}</td>
                  <td className="px-5 py-3">
                    <span className={`rounded-full border px-2 py-0.5 text-xs font-medium capitalize ${statusStyle(h.status)}`}>
                      {h.status}
                    </span>
                  </td>
                  <td className="px-5 py-3">
                    <PhaseBadge phase={h.cert_status} />
                  </td>
                  {canEdit && (
                    <td className="px-5 py-3 text-right">
                      <button
                        onClick={() => handleDetach(h)}
                        disabled={detachingId === h.id}
                        className="rounded-lg border border-red-200 px-3 py-1.5 text-xs font-medium text-red-600 hover:bg-red-50 disabled:opacity-50 transition-colors"
                      >
                        {detachingId === h.id ? "Detaching…" : "Detach"}
                      </button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

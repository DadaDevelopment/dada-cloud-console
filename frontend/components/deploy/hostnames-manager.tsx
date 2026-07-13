"use client";
import { useEffect, useState, FormEvent } from "react";
import Link from "next/link";
import { customDomainsApi } from "@/lib/api";
import type { DomainAuthorization, DomainHostname } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { ManagedDnsPanel } from "@/components/deploy/managed-dns";
import { useT } from "@/lib/i18n/console/context";

type ConnectPath = "advanced" | "delegate";

// Level 2 of the Vercel-style model: attach a hostname (apex or subdomain under
// an already-verified apex authorization) to this specific app + environment.
// Ownership/anti-hijack is enforced server-side — the apex must be verified for
// THIS project before any of its hostnames can be attached here.

function appHref(host: string): string {
  return "https:" + "/".repeat(2) + host;
}

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
  verifiedApexes: DomainAuthorization[];
}

export function HostnamesManager({ projectId, envId, appName, canEdit, verifiedApexes }: Props) {
  const { t } = useT();
  const [path, setPath] = useState<ConnectPath>("delegate");
  const [delegateAuthId, setDelegateAuthId] = useState(verifiedApexes[0]?.id ?? "");
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
      .catch((err) => setError(err instanceof Error ? err.message : t("domains.hm.loadError")))
      .finally(() => setIsLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
      setSubmitError(err instanceof Error ? err.message : t("domains.hm.attachError"));
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleDetach(h: DomainHostname) {
    if (!confirm(t("domains.hm.confirmDetach", { name: h.hostname }))) return;
    setDetachingId(h.id);
    setError(null);
    try {
      await customDomainsApi.detachHostname(projectId, envId, appName, h.id);
      setHostnames((prev) => prev.filter((x) => x.id !== h.id));
    } catch (err) {
      setError(err instanceof Error ? err.message : t("domains.hm.detachError"));
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

  const toggleBtn = (value: ConnectPath, label: string, hint: string) => (
    <button
      type="button"
      onClick={() => setPath(value)}
      aria-pressed={path === value}
      className={`flex-1 rounded-lg px-4 py-2.5 text-sm font-medium transition-colors ${
        path === value
          ? "bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100 shadow-sm"
          : "text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200"
      }`}
    >
      {label}
      <span className="ml-2 text-xs font-normal text-gray-400 dark:text-gray-500">{hint}</span>
    </button>
  );

  return (
    <div className="space-y-6">
      <div>
        <p className="mb-1.5 text-xs font-medium text-gray-500 dark:text-gray-400">{t("domains.path.toggleLabel")}</p>
        <div className="flex gap-1 rounded-xl border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900 p-1">
          {toggleBtn("delegate", t("domains.path.delegate"), t("domains.path.delegateHint"))}
          {toggleBtn("advanced", t("domains.path.advanced"), t("domains.path.advancedHint"))}
        </div>
      </div>

      {path === "delegate" ? (
        verifiedApexes.length === 0 ? (
          <div className="rounded-lg border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 px-5 py-6 text-sm text-gray-500 dark:text-gray-400">
            {t("domains.dns.needVerified")}
          </div>
        ) : (
          <div className="space-y-4">
            {verifiedApexes.length > 1 && (
              <div>
                <label className="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-200">{t("domains.dns.pickApex")}</label>
                <select
                  value={delegateAuthId}
                  onChange={(e) => setDelegateAuthId(e.target.value)}
                  className="w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2.5 text-sm text-gray-900 dark:text-gray-100 focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500/30"
                >
                  {verifiedApexes.map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.apex_domain}
                    </option>
                  ))}
                </select>
              </div>
            )}
            {(() => {
              const selected = verifiedApexes.find((a) => a.id === delegateAuthId) ?? verifiedApexes[0];
              return (
                <ManagedDnsPanel
                  key={selected.id}
                  projectId={projectId}
                  authId={selected.id}
                  apex={selected.apex_domain}
                  canEdit={canEdit}
                />
              );
            })()}
          </div>
        )
      ) : (
      <>
      <div className="rounded-xl border border-gray-200 bg-white px-5 py-6">
        <h2 className="text-lg font-semibold text-gray-900">{t("domains.hm.title")}</h2>
        <p className="mt-1 text-sm text-gray-500">
          {t("domains.hm.subtitle")} {t("domains.hm.authorizePre")}{" "}
          <Link href={`/projects/${projectId}/domains`} className="font-medium text-blue-600 hover:text-blue-700">
            {t("domains.hm.authorizeLink")}
          </Link>{" "}
          {t("domains.hm.authorizePost")}
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
              title={t("domains.hm.inputTitle")}
              className="min-w-64 flex-1 rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <button
              type="submit"
              disabled={isSubmitting || !envId}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {isSubmitting ? <Spinner size="sm" /> : null}
              {t("domains.hm.attach")}
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
            <p className="font-medium">{t("domains.hm.dnsTitle")}</p>
            <p className="mt-1 font-mono text-xs">
              {dnsHint.type} {dnsHint.host} → {dnsHint.target}
            </p>
            <p className="mt-1 text-xs text-blue-700">
              {t("domains.hm.dnsNote")}
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
          {t("domains.hm.empty")}
        </div>
      ) : (
        <div className="overflow-x-auto rounded-xl border border-gray-200 bg-white">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-left text-xs font-medium text-gray-500">
              <tr>
                <th className="px-5 py-3">{t("domains.hm.thHostname")}</th>
                <th className="px-5 py-3">{t("domains.hm.thRecord")}</th>
                <th className="px-5 py-3">{t("domains.hm.thStatus")}</th>
                <th className="px-5 py-3">{t("domains.hm.thCert")}</th>
                {canEdit && <th className="px-5 py-3" />}
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {hostnames.map((h) => (
                <tr key={h.id}>
                  <td className="px-5 py-3 font-mono text-gray-900">
                    <a
                      href={appHref(h.hostname)}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="text-blue-600 hover:text-blue-700 hover:underline"
                    >
                      {h.hostname}
                    </a>
                    {h.managed && (
                      <span className="ml-2 rounded-full border border-gray-200 bg-gray-50 px-2 py-0.5 text-xs font-medium text-gray-600">
                        {t("domains.hm.defaultBadge")}
                      </span>
                    )}
                  </td>
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
                      {!h.managed && (
                        <button
                          onClick={() => handleDetach(h)}
                          disabled={detachingId === h.id}
                          className="rounded-lg border border-red-200 px-3 py-1.5 text-xs font-medium text-red-600 hover:bg-red-50 disabled:opacity-50 transition-colors"
                        >
                          {detachingId === h.id ? t("domains.hm.detaching") : t("domains.hm.detach")}
                        </button>
                      )}
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      </>
      )}
    </div>
  );
}

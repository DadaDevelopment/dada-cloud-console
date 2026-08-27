"use client";
import { useEffect, useState, FormEvent } from "react";
import { useParams, useSearchParams, useRouter } from "next/navigation";
import { cachesApi } from "@/lib/api";
import type { ResourceSnapshot, CacheCredentialsResponse } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { CopyButton } from "@/components/ui/copy-button";
import { Modal } from "@/components/ui/modal";
import { useProjectContext } from "@/lib/project-context";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { canMutate } from "@/lib/rbac";
import { maskDsnPassword } from "@/lib/dsn";
import {
  credentialsRetryBudgetMs,
  revealCredentialsWithProvisionRetry,
  type CredentialsRetryProgress,
  type DatabaseCredentialsErrorKind,
} from "@/lib/db-credentials-retry";
import { useT } from "@/lib/i18n/console/context";

interface CacheSpec {
  appRef?: string;
  keyPrefix?: string;
  profile?: string;
  namespace?: string;
}
interface CacheSummary {
  spec?: CacheSpec;
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{label}</p>
      <div className="mt-1 text-sm font-medium text-gray-900 dark:text-gray-100">{children}</div>
    </div>
  );
}

export default function CacheDetailPage() {
  const params = useParams<{ projectId: string; name: string }>();
  const search = useSearchParams();
  const router = useRouter();
  const [refreshTick, setRefreshTick] = useState(0);
  const { projectId, name } = params;
  const { project, selectedEnv, role } = useProjectContext();
  const { t } = useT();
  const envId = search.get("envId") || selectedEnv?.id || "";
  const canManage = canMutate(role);

  const [cache, setCache] = useState<ResourceSnapshot | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isDeleteOpen, setIsDeleteOpen] = useState(false);
  const [deleteConfirmName, setDeleteConfirmName] = useState("");
  const [isDeleteSubmitting, setIsDeleteSubmitting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const [creds, setCreds] = useState<CacheCredentialsResponse | null>(null);
  const [credsLoading, setCredsLoading] = useState(false);
  const [revealPw, setRevealPw] = useState(false);
  const [credsError, setCredsError] = useState<{ kind: DatabaseCredentialsErrorKind; message?: string } | null>(null);
  const [credsRetry, setCredsRetry] = useState<CredentialsRetryProgress | null>(null);

  async function revealCreds() {
    setCredsLoading(true);
    setCredsError(null);
    setCredsRetry(null);
    const result = await revealCredentialsWithProvisionRetry(
      () => cachesApi.credentials(projectId, envId, name),
      { onRetry: setCredsRetry },
    );
    setCredsRetry(null);
    setCredsLoading(false);
    if (result.ok) {
      setCreds(result.value);
      return;
    }
    setCredsError({ kind: result.kind, message: credsErrorMessage(result) });
  }

  function credsErrorMessage(result: { ok: false; kind: DatabaseCredentialsErrorKind; error: unknown; retries: number }): string {
    if (result.kind === "notConfigured") return t("caches.detail.access.notConfigured");
    if (result.kind === "generic") {
      return result.error instanceof Error ? result.error.message : t("caches.detail.access.error");
    }
    if (result.retries === 0) return t("caches.detail.access.notReady");
    return t("caches.detail.access.notReadyExhausted", {
      minutes: Math.round(credentialsRetryBudgetMs() / 60_000),
    });
  }

  useEffect(() => {
    if (!envId) {
      if (!selectedEnv) return;
    }
    if (!envId) return;
    let cancelled = false;
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setIsLoading(true);
    cachesApi
      .list(projectId, envId)
      .then((data) => {
        if (cancelled) return;
        const found = (data.caches ?? []).find((d) => d.name === name);
        if (!found) setError(t("caches.error.notFound"));
        else setCache(found);
      })
      .catch((err) => !cancelled && setError(err instanceof Error ? err.message : t("caches.error.loadDetail")))
      .finally(() => !cancelled && setIsLoading(false));
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, name, envId, selectedEnv, refreshTick]);

  function closeDelete() {
    setIsDeleteOpen(false);
    setDeleteConfirmName("");
    setDeleteError(null);
  }

  async function submitDelete(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (deleteConfirmName !== name) return;
    setDeleteError(null);
    setIsDeleteSubmitting(true);
    try {
      await cachesApi.remove(projectId, envId, name);
      closeDelete();
      router.push(`/projects/${projectId}/redis`);
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : t("caches.delete.error"));
    } finally {
      setIsDeleteSubmitting(false);
    }
  }

  if (isLoading) {
    return <div className="flex h-64 items-center justify-center"><Spinner size="lg" /></div>;
  }
  if (error || !cache) {
    return (
      <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
        {error ?? t("caches.error.notFound")}
      </div>
    );
  }

  const summary = (cache.summary_json ?? {}) as CacheSummary;
  const spec = summary.spec ?? {};
  const revealedHost = creds?.host ?? "";
  const connPort = creds?.port || "6379";

  return (
    <div>
      <div className="mb-8 flex flex-wrap items-start justify-between gap-3">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.projects"), href: "/projects" },
              { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
              { label: t("nav.redis"), href: `/projects/${projectId}/redis${envId ? `?env=${envId}` : ""}` },
              { label: cache.name },
            ]}
          />
          <div className="mt-2 flex items-center gap-3">
            <h1 className="font-mono text-2xl font-bold text-gray-900 dark:text-gray-100">{cache.name}</h1>
            <PhaseBadge phase={cache.phase} />
          </div>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("caches.detail.subtitle")}</p>
        </div>
        {canManage && (
          <div className="flex flex-wrap items-center gap-2">
            <button
              onClick={() => setIsDeleteOpen(true)}
              className="inline-flex items-center gap-2 rounded-lg border border-red-200 dark:border-red-900 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm font-medium text-red-600 dark:text-red-400 hover:bg-red-50 transition-colors shadow-sm"
            >
              {t("common.delete")}
            </button>
          </div>
        )}
      </div>

      <section className="mb-8">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("caches.detail.overview")}</h2>
        <div className="grid gap-4 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm sm:grid-cols-2 lg:grid-cols-4">
          <Field label={t("caches.detail.field.attachedApp")}>{spec.appRef ? <span className="font-mono">{spec.appRef}</span> : "—"}</Field>
          <Field label={t("caches.detail.field.keyPrefix")}><span className="font-mono">{spec.keyPrefix ?? "—"}</span></Field>
          <Field label={t("caches.detail.field.profile")}>{spec.profile ?? "—"}</Field>
          <Field label={t("caches.detail.field.status")}>{cache.phase || t("caches.detail.field.statusUnknown")}</Field>
        </div>
      </section>

      <section className="mb-8">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("caches.detail.connection")}</h2>
        <div className="space-y-4 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm">
          <div className="flex items-center justify-between gap-3">
            <Field label={t("caches.detail.field.host")}>
              {revealedHost
                ? <span className="font-mono text-xs sm:text-sm">{revealedHost}</span>
                : <span className="font-mono text-xs sm:text-sm text-gray-400 dark:text-gray-500">{t("caches.detail.hostHidden")}</span>}
            </Field>
            {revealedHost && <CopyButton value={revealedHost} />}
          </div>
          <Field label={t("caches.detail.field.port")}><span className="font-mono">{connPort}</span></Field>
          {!creds && (
            <p className="text-xs text-gray-400 dark:text-gray-500">{t("caches.detail.hostHint")}</p>
          )}
          {creds ? (
            <div className="space-y-3 border-t border-gray-100 dark:border-gray-800 pt-4">
              {creds.dsn && (
                <div>
                  <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("caches.detail.access.dsn")}</p>
                  <div className="mt-1 flex items-center gap-2">
                    <code className="flex-1 break-all rounded-md border border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/30 px-3 py-2 font-mono text-xs text-blue-900 dark:text-blue-200">
                      {revealPw ? creds.dsn : maskDsnPassword(creds.dsn)}
                    </code>
                    <CopyButton value={creds.dsn} />
                  </div>
                  <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{t("caches.detail.access.dsnHint")}</p>
                </div>
              )}
              <div>
                <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("caches.detail.access.username")}</p>
                <div className="mt-1 flex items-center gap-2">
                  <code className="flex-1 break-all rounded-md border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900 px-3 py-2 font-mono text-xs text-gray-800 dark:text-gray-200">
                    {creds.username}
                  </code>
                  <CopyButton value={creds.username} />
                </div>
              </div>
              <div>
                <div className="flex items-center justify-between">
                  <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("caches.detail.access.password")}</p>
                  <button
                    type="button"
                    onClick={() => setRevealPw((v) => !v)}
                    className="text-xs font-medium text-blue-600 hover:text-blue-700"
                  >
                    {revealPw ? t("caches.detail.access.hide") : t("caches.detail.access.reveal")}
                  </button>
                </div>
                <div className="mt-1 flex items-center gap-2">
                  <code className="flex-1 break-all rounded-md border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900 px-3 py-2 font-mono text-xs text-gray-800 dark:text-gray-200">
                    {revealPw ? creds.password : "•".repeat(Math.min(creds.password.length, 40))}
                  </code>
                  <CopyButton value={creds.password} />
                </div>
              </div>
            </div>
          ) : (
            <div className="border-t border-gray-100 dark:border-gray-800 pt-4">
              <p className="mb-3 text-xs text-gray-500 dark:text-gray-400">{t("caches.detail.credentials")}</p>
              {canManage ? (
                <>
                  <button
                    onClick={revealCreds}
                    disabled={credsLoading}
                    className="inline-flex items-center gap-2 rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
                  >
                    {credsLoading ? (
                      <>
                        <Spinner size="sm" />
                        {credsRetry
                          ? t("databases.detail.access.provisioning", { attempt: credsRetry.attempt, total: credsRetry.totalAttempts })
                          : t("caches.detail.access.revealing")}
                      </>
                    ) : (
                      t("caches.detail.access.revealBtn")
                    )}
                  </button>
                  {credsError && (
                    <p className="mt-2 text-xs text-red-600 dark:text-red-400">{credsError.message}</p>
                  )}
                </>
              ) : (
                <p className="text-xs text-gray-400 dark:text-gray-500">{t("databases.detail.access.none")}</p>
              )}
            </div>
          )}
        </div>
      </section>

      <Modal isOpen={isDeleteOpen} onClose={closeDelete} title={t("caches.delete.modal.title")}>
        <form onSubmit={submitDelete} className="space-y-4">
          <p className="text-sm text-gray-600 dark:text-gray-300">
            {t("caches.delete.modal.body", { name: cache.name })}
          </p>
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {t("caches.delete.modal.confirmLabel", { name: cache.name })}
            </label>
            <input
              type="text"
              value={deleteConfirmName}
              onChange={(e) => setDeleteConfirmName(e.target.value)}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 font-mono text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-red-500 focus:outline-none focus:ring-1 focus:ring-red-500"
            />
            {deleteConfirmName && deleteConfirmName !== cache.name && (
              <p className="mt-1 text-xs text-red-600 dark:text-red-400">{t("caches.delete.modal.mismatch")}</p>
            )}
          </div>
          {deleteError && (
            <div role="alert" className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
              {deleteError}
            </div>
          )}
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={closeDelete}
              className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 transition-colors"
            >
              {t("common.cancel")}
            </button>
            <button
              type="submit"
              disabled={isDeleteSubmitting || deleteConfirmName !== cache.name}
              className="inline-flex items-center gap-2 rounded-lg bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {isDeleteSubmitting ? <><Spinner size="sm" /> {t("common.deleting")}</> : t("common.delete")}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}

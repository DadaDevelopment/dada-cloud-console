"use client";
import { useCallback, useEffect, useState, FormEvent } from "react";
import { useParams, useSearchParams, useRouter } from "next/navigation";
import { databasesApi } from "@/lib/api";
import type { ResourceSnapshot, DBBackup, DatabaseCredentialsResponse } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { CopyButton } from "@/components/ui/copy-button";
import { Modal } from "@/components/ui/modal";
import { useProjectContext } from "@/lib/project-context";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { StateChip } from "@/components/ui/state-chip";
import type { ChipTone } from "@/components/ui/state-chip";
import { canMutate } from "@/lib/rbac";
import { timeAgo } from "@/lib/format";
import { useT } from "@/lib/i18n/console/context";
import { DbInsights } from "@/components/databases/db-insights";
import { DbActivity } from "@/components/databases/db-activity";

interface DbSpec {
  database?: string;
  appRef?: string;
  namespace?: string;
  backup?: { enabled?: boolean; frequency?: string; retention?: string };
}
interface DbSummary {
  database?: string;
  app_ref?: string;
  namespace?: string;
  spec?: DbSpec;
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{label}</p>
      <div className="mt-1 text-sm font-medium text-gray-900 dark:text-gray-100">{children}</div>
    </div>
  );
}

function ErrorBox({ text }: { text: string }) {
  return (
    <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">{text}</div>
  );
}

function ModalFooter({
  onCancel, submitting, submitLabel, tone = "blue", disabled = false, submittingLabel,
}: {
  onCancel: () => void; submitting: boolean; submitLabel: string;
  tone?: "blue" | "red" | "purple"; disabled?: boolean; submittingLabel?: string;
}) {
  const { t } = useT();
  const tones = {
    blue: "bg-blue-600 hover:bg-blue-700",
    red: "bg-red-600 hover:bg-red-700",
    purple: "bg-purple-600 hover:bg-purple-700",
  };
  return (
    <div className="flex justify-end gap-3 pt-2">
      <button
        type="button" onClick={onCancel}
        className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 transition-colors"
      >
        {t("common.cancel")}
      </button>
      <button
        type="submit" disabled={submitting || disabled}
        className={`inline-flex items-center gap-2 rounded-lg px-4 py-2 text-sm font-medium text-white ${tones[tone]} disabled:cursor-not-allowed disabled:opacity-50 transition-colors`}
      >
        {submitting ? <><Spinner size="sm" /> {submittingLabel ?? t("common.deleting")}</> : submitLabel}
      </button>
    </div>
  );
}

function backupStatusTone(status: string): ChipTone {
  switch (status) {
    case "Ready": return "ready";
    case "Failed": return "error";
    case "Running":
    case "Pending": return "needs-action";
    default: return "neutral";
  }
}

function backupStatusLabel(status: string): string {
  switch (status) {
    case "Ready": return "databases.backups.status.ready";
    case "Running": return "databases.backups.status.running";
    case "Pending": return "databases.backups.status.pending";
    case "Failed": return "databases.backups.status.failed";
    case "Deleting": return "databases.backups.status.deleting";
    case "Deleted": return "databases.backups.status.deleted";
    default: return status;
  }
}

function backupKindLabel(kind: string): string {
  switch (kind) {
    case "manual": return "databases.backups.kind.manual";
    case "scheduled": return "databases.backups.kind.scheduled";
    case "pre-restore": return "databases.backups.kind.preRestore";
    default: return kind;
  }
}

function fmtBackupBytes(v: number): string {
  if (v >= 1 << 30) return `${(v / (1 << 30)).toFixed(1)} GB`;
  if (v >= 1 << 20) return `${(v / (1 << 20)).toFixed(0)} MB`;
  if (v >= 1 << 10) return `${(v / (1 << 10)).toFixed(0)} KB`;
  return `${v} B`;
}

export default function DatabaseDetailPage() {
  const params = useParams<{ projectId: string; name: string }>();
  const search = useSearchParams();
  const router = useRouter();
  const [refreshTick, setRefreshTick] = useState(0);
  const { projectId, name } = params;
  const { project, selectedEnv, role } = useProjectContext();
  const { t } = useT();
  const envId = search.get("envId") || selectedEnv?.id || "";
  const canManage = canMutate(role);

  const [db, setDb] = useState<ResourceSnapshot | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isDeleteOpen, setIsDeleteOpen] = useState(false);
  const [isDeleteSubmitting, setIsDeleteSubmitting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const [backups, setBackups] = useState<DBBackup[]>([]);
  const [isBackupsLoading, setIsBackupsLoading] = useState(false);
  const [backupsError, setBackupsError] = useState<string | null>(null);
  const [isCreatingBackup, setIsCreatingBackup] = useState(false);
  const [downloadingBackupId, setDownloadingBackupId] = useState<string | null>(null);

  const [restoreTarget, setRestoreTarget] = useState<DBBackup | null>(null);
  const [restoreConfirmName, setRestoreConfirmName] = useState("");
  const [isRestoreSubmitting, setIsRestoreSubmitting] = useState(false);
  const [restoreError, setRestoreError] = useState<string | null>(null);

  const [creds, setCreds] = useState<DatabaseCredentialsResponse | null>(null);
  const [credsLoading, setCredsLoading] = useState(false);
  const [revealPw, setRevealPw] = useState(false);
  const [credsError, setCredsError] = useState<{ kind: "notReady" | "notConfigured" | "generic"; message?: string } | null>(null);

  async function revealCreds() {
    setCredsLoading(true);
    setCredsError(null);
    try {
      const r = await databasesApi.credentials(projectId, envId, name);
      setCreds(r);
    } catch (e) {
      const status = (e as { status?: number } | undefined)?.status;
      if (status === 404) setCredsError({ kind: "notReady" });
      else if (status === 503) setCredsError({ kind: "notConfigured" });
      else setCredsError({ kind: "generic", message: e instanceof Error ? e.message : t("databases.detail.access.error") });
    } finally {
      setCredsLoading(false);
    }
  }

  useEffect(() => {
    if (!envId) {
      if (!selectedEnv) return;
    }
    if (!envId) return;
    let cancelled = false;
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setIsLoading(true);
    databasesApi
      .list(projectId, envId)
      .then((data) => {
        if (cancelled) return;
        const found = (data.databases ?? []).find((d) => d.name === name);
        if (!found) setError(t("databases.error.notFound"));
        else setDb(found);
      })
      .catch((err) => !cancelled && setError(err instanceof Error ? err.message : t("databases.error.loadDetail")))
      .finally(() => !cancelled && setIsLoading(false));
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, name, envId, selectedEnv, refreshTick]);

  function gotoOp() {
    setTimeout(() => setRefreshTick((v) => v + 1), 1500);
  }

  const loadBackups = useCallback(
    async (silent = false) => {
      if (!envId || !canManage) return;
      if (!silent) setIsBackupsLoading(true);
      try {
        const data = await databasesApi.listBackups(projectId, envId, name);
        setBackups(data.backups ?? []);
        setBackupsError(null);
      } catch (err) {
        setBackupsError(err instanceof Error ? err.message : t("databases.backups.error"));
      } finally {
        if (!silent) setIsBackupsLoading(false);
      }
    },
    [projectId, envId, name, canManage, t]
  );

  useEffect(() => {
    void loadBackups(); // eslint-disable-line react-hooks/set-state-in-effect
  }, [loadBackups]);

  useEffect(() => {
    if (!canManage) return;
    const hasActive = backups.some((b) => b.status === "Pending" || b.status === "Running");
    if (!hasActive) return;
    const interval = setInterval(() => void loadBackups(true), 4000);
    return () => clearInterval(interval);
  }, [backups, canManage, loadBackups]);

  async function handleCreateBackup() {
    setIsCreatingBackup(true);
    setBackupsError(null);
    try {
      const { backup: created } = await databasesApi.createBackup(projectId, envId, name);
      setBackups((prev) => [created, ...prev]);
    } catch (err) {
      setBackupsError(err instanceof Error ? err.message : t("databases.backups.createError"));
    } finally {
      setIsCreatingBackup(false);
    }
  }

  async function handleDownloadBackup(b: DBBackup) {
    setDownloadingBackupId(b.id);
    setBackupsError(null);
    try {
      const { url } = await databasesApi.downloadBackup(projectId, envId, name, b.id);
      window.location.assign(url);
    } catch (err) {
      setBackupsError(err instanceof Error ? err.message : t("databases.backups.downloadError"));
    } finally {
      setDownloadingBackupId(null);
    }
  }

  function openRestore(target: DBBackup) {
    setRestoreTarget(target);
    setRestoreConfirmName("");
    setRestoreError(null);
  }

  function closeRestore() {
    setRestoreTarget(null);
    setRestoreConfirmName("");
    setRestoreError(null);
  }

  async function submitRestore(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!restoreTarget || restoreConfirmName !== name) return;
    setRestoreError(null);
    setIsRestoreSubmitting(true);
    try {
      await databasesApi.restore(projectId, envId, name, restoreTarget.id);
      closeRestore();
      gotoOp();
    } catch (err) {
      setRestoreError(err instanceof Error ? err.message : t("databases.backups.restoreError"));
    } finally {
      setIsRestoreSubmitting(false);
    }
  }

  async function submitDelete(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setDeleteError(null);
    setIsDeleteSubmitting(true);
    try {
      await databasesApi.remove(projectId, envId, name);
      setIsDeleteOpen(false);
      router.push(`/projects/${projectId}/databases`);
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : t("databases.delete.error"));
    } finally {
      setIsDeleteSubmitting(false);
    }
  }

  if (isLoading) {
    return <div className="flex h-64 items-center justify-center"><Spinner size="lg" /></div>;
  }
  if (error || !db) {
    return (
      <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
        {error ?? t("databases.error.notFound")}
      </div>
    );
  }

  const summary = (db.summary_json ?? {}) as DbSummary;
  const spec = summary.spec ?? {};
  const dbName = summary.database ?? spec.database ?? db.name;
  const appRef = summary.app_ref ?? spec.appRef;
  const backup = spec.backup;
  const backupOn = !!backup?.enabled;
  const revealedHost = creds?.host ?? "";
  const connPort = creds?.port || "5432";
  const connDbName = creds?.database || dbName;

  return (
    <div>
      <div className="mb-8 flex flex-wrap items-start justify-between gap-3">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.projects"), href: "/projects" },
              { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
              { label: t("nav.databases"), href: `/projects/${projectId}/databases${envId ? `?env=${envId}` : ""}` },
              { label: db.name },
            ]}
          />
          <div className="mt-2 flex items-center gap-3">
            <h1 className="font-mono text-2xl font-bold text-gray-900 dark:text-gray-100">{db.name}</h1>
            <PhaseBadge phase={db.phase} />
          </div>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("databases.detail.subtitle")}</p>
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
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("databases.detail.overview")}</h2>
        <div className="grid gap-4 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm sm:grid-cols-2 lg:grid-cols-4">
          <Field label={t("databases.detail.field.database")}>{dbName}</Field>
          <Field label={t("databases.detail.field.attachedApp")}>{appRef ? <span className="font-mono">{appRef}</span> : "—"}</Field>
          <Field label={t("databases.detail.field.environment")}>{selectedEnv?.name ?? "—"}</Field>
          <Field label={t("databases.detail.field.status")}>{db.phase || t("databases.detail.field.statusUnknown")}</Field>
        </div>
      </section>

      <DbInsights projectId={projectId} envId={envId} name={name} />
      <DbActivity projectId={projectId} envId={envId} name={name} canManage={canManage} />

      <section className="mb-8">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("databases.detail.connection")}</h2>
        <div className="space-y-4 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm">
          <div className="flex items-center justify-between gap-3">
            <Field label={t("databases.detail.field.host")}>
              {revealedHost
                ? <span className="font-mono text-xs sm:text-sm">{revealedHost}</span>
                : <span className="font-mono text-xs sm:text-sm text-gray-400 dark:text-gray-500">{t("databases.detail.hostHidden")}</span>}
            </Field>
            {revealedHost && <CopyButton value={revealedHost} />}
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t("databases.detail.field.dbName")}><span className="font-mono">{connDbName}</span></Field>
            <Field label={t("databases.detail.field.port")}><span className="font-mono">{connPort}</span></Field>
          </div>
          {!creds && (
            <p className="text-xs text-gray-400 dark:text-gray-500">{t("databases.detail.hostHint")}</p>
          )}
          {creds ? (
            <div className="space-y-3 border-t border-gray-100 dark:border-gray-800 pt-4">
              <div>
                <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("databases.detail.access.username")}</p>
                <div className="mt-1 flex items-center gap-2">
                  <code className="flex-1 break-all rounded-md border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900 px-3 py-2 font-mono text-xs text-gray-800 dark:text-gray-200">
                    {creds.username}
                  </code>
                  <CopyButton value={creds.username} />
                </div>
              </div>
              <div>
                <div className="flex items-center justify-between">
                  <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("databases.detail.access.password")}</p>
                  <button
                    type="button"
                    onClick={() => setRevealPw((v) => !v)}
                    className="text-xs font-medium text-blue-600 hover:text-blue-700"
                  >
                    {revealPw ? t("databases.detail.access.hide") : t("databases.detail.access.reveal")}
                  </button>
                </div>
                <div className="mt-1 flex items-center gap-2">
                  <code className="flex-1 break-all rounded-md border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900 px-3 py-2 font-mono text-xs text-gray-800 dark:text-gray-200">
                    {revealPw ? creds.password : "•".repeat(Math.min(creds.password.length, 40))}
                  </code>
                  <CopyButton value={creds.password} />
                </div>
              </div>
              {creds.external_host && (
                <div>
                  <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("databases.detail.access.externalHost")}</p>
                  <div className="mt-1 flex items-center gap-2">
                    <code className="flex-1 break-all rounded-md border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/30 px-3 py-2 font-mono text-xs text-amber-800 dark:text-amber-300">
                      {creds.external_host}:{creds.external_port || creds.port}
                    </code>
                    <CopyButton value={`${creds.external_host}:${creds.external_port || creds.port}`} />
                  </div>
                  <p className="mt-1 text-xs text-amber-600 dark:text-amber-500">{t("databases.detail.access.externalWarning")}</p>
                </div>
              )}
            </div>
          ) : (
            <div className="border-t border-gray-100 dark:border-gray-800 pt-4">
              <p className="mb-3 text-xs text-gray-500 dark:text-gray-400">{t("databases.detail.credentials")}</p>
              {canManage ? (
                <>
                  <button
                    type="button"
                    onClick={revealCreds}
                    disabled={credsLoading}
                    className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50 transition-colors"
                  >
                    {credsLoading ? <><Spinner size="sm" /> {t("databases.detail.access.revealing")}</> : t("databases.detail.access.revealBtn")}
                  </button>
                  {credsError && (
                    <p className={`mt-3 text-sm ${credsError.kind === "generic" ? "text-red-600 dark:text-red-400" : "text-gray-500 dark:text-gray-400"}`}>
                      {credsError.kind === "notReady"
                        ? t("databases.detail.access.notReady")
                        : credsError.kind === "notConfigured"
                          ? t("databases.detail.access.notConfigured")
                          : credsError.message}
                    </p>
                  )}
                </>
              ) : (
                <p className="text-sm text-gray-500 dark:text-gray-400">{t("databases.detail.access.none")}</p>
              )}
            </div>
          )}
        </div>
      </section>

      <section className="mb-8">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("databases.detail.backups")}</h2>
        <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm">
          {backupOn ? (
            <div className="grid gap-4 sm:grid-cols-3">
              <Field label={t("databases.detail.backup.field.status")}>
                <span className="inline-flex items-center gap-1.5 text-green-700">
                  <span className="h-2 w-2 rounded-full bg-green-500" /> {t("databases.detail.backup.enabled")}
                </span>
              </Field>
              <Field label={t("databases.detail.backup.field.schedule")}>{backup?.frequency ?? "—"}</Field>
              <Field label={t("databases.detail.backup.field.retention")}>{backup?.retention ?? "—"}</Field>
            </div>
          ) : (
            <div className="flex items-center gap-2 text-sm text-gray-500 dark:text-gray-400">
              <span className="h-2 w-2 rounded-full bg-gray-300 dark:bg-gray-700" />
              {t("databases.detail.backup.disabled")}
            </div>
          )}
        </div>
      </section>

      {canManage && (
        <section>
          <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
            <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("databases.backups.title")}</h2>
            <button
              onClick={handleCreateBackup}
              disabled={isCreatingBackup}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
            >
              {isCreatingBackup ? <><Spinner size="sm" /> {t("databases.backups.creating")}</> : t("databases.backups.createBtn")}
            </button>
          </div>

          {backupsError && <div className="mb-3"><ErrorBox text={backupsError} /></div>}

          {isBackupsLoading ? (
            <div className="flex h-24 items-center justify-center rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900">
              <Spinner />
            </div>
          ) : backups.length === 0 ? (
            <div className="flex items-center gap-2 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm text-sm text-gray-500 dark:text-gray-400">
              {t("databases.backups.empty")}
            </div>
          ) : (
            <div className="overflow-x-auto rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 shadow-sm">
              <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-800">
                <thead className="bg-gray-50 dark:bg-gray-900">
                  <tr>
                    <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("databases.backups.column.created")}</th>
                    <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("databases.backups.column.status")}</th>
                    <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("databases.backups.column.kind")}</th>
                    <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("databases.backups.column.size")}</th>
                    <th className="px-5 py-3 text-right text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400" />
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                  {backups.map((b) => (
                    <tr key={b.id}>
                      <td className="px-5 py-3 text-xs text-gray-500 dark:text-gray-400">{timeAgo(b.created_at)}</td>
                      <td className="px-5 py-3 text-sm">
                        <StateChip tone={backupStatusTone(b.status)} dot>{t(backupStatusLabel(b.status))}</StateChip>
                      </td>
                      <td className="px-5 py-3 text-sm text-gray-600 dark:text-gray-400">{t(backupKindLabel(b.kind))}</td>
                      <td className="px-5 py-3 text-sm text-gray-600 dark:text-gray-400">
                        {typeof b.size_bytes === "number" ? fmtBackupBytes(b.size_bytes) : "—"}
                      </td>
                      <td className="px-5 py-3 text-right">
                        {b.status === "Ready" && (
                          <div className="flex items-center justify-end gap-4">
                            <button
                              onClick={() => handleDownloadBackup(b)}
                              disabled={downloadingBackupId === b.id}
                              className="text-xs font-medium text-blue-600 dark:text-blue-400 hover:text-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
                            >
                              {downloadingBackupId === b.id ? t("databases.backups.downloading") : t("databases.backups.download")}
                            </button>
                            <button
                              onClick={() => openRestore(b)}
                              className="text-xs font-medium text-blue-600 dark:text-blue-400 hover:text-blue-700"
                            >
                              {t("databases.backups.restore")}
                            </button>
                          </div>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      )}

      <Modal
        isOpen={isDeleteOpen}
        onClose={() => { setIsDeleteOpen(false); setDeleteError(null); }}
        title={t("databases.delete.modal.title")}
      >
        <form onSubmit={submitDelete} className="space-y-4">
          <p className="text-sm text-gray-600 dark:text-gray-400">
            {t("databases.delete.modal.body", { name: db.name })}
          </p>
          {deleteError && <ErrorBox text={deleteError} />}
          <ModalFooter onCancel={() => setIsDeleteOpen(false)} submitting={isDeleteSubmitting} submitLabel={t("common.delete")} tone="red" />
        </form>
      </Modal>

      <Modal
        isOpen={!!restoreTarget}
        onClose={closeRestore}
        title={t("databases.backups.restoreModal.title")}
      >
        <form onSubmit={submitRestore} className="space-y-4">
          <p className="text-sm text-gray-600 dark:text-gray-400">
            {t("databases.backups.restoreModal.body", { name })}
          </p>
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {t("databases.backups.restoreModal.confirmLabel", { name })}
            </label>
            <input
              type="text"
              required
              value={restoreConfirmName}
              onChange={(e) => setRestoreConfirmName(e.target.value)}
              placeholder={name}
              className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            {restoreConfirmName.length > 0 && restoreConfirmName !== name && (
              <p className="mt-1 text-xs text-red-600 dark:text-red-400">{t("databases.backups.restoreModal.mismatch")}</p>
            )}
          </div>
          {restoreError && <ErrorBox text={restoreError} />}
          <ModalFooter
            onCancel={closeRestore}
            submitting={isRestoreSubmitting}
            submittingLabel={t("databases.backups.restoreModal.restoring")}
            submitLabel={t("databases.backups.restoreModal.submit")}
            tone="red"
            disabled={restoreConfirmName !== name}
          />
        </form>
      </Modal>
    </div>
  );
}

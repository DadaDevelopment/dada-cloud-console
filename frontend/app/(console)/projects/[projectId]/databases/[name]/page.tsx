"use client";
import { useEffect, useState } from "react";
import { useParams, useSearchParams } from "next/navigation";
import { databasesApi } from "@/lib/api";
import type { ResourceSnapshot } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { CopyButton } from "@/components/ui/copy-button";
import { useProjectContext } from "@/lib/project-context";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { useT } from "@/lib/i18n/console/context";

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
      <p className="text-xs font-semibold uppercase tracking-wide text-gray-400">{label}</p>
      <div className="mt-1 text-sm font-medium text-gray-900">{children}</div>
    </div>
  );
}

export default function DatabaseDetailPage() {
  const params = useParams<{ projectId: string; name: string }>();
  const search = useSearchParams();
  const { projectId, name } = params;
  const { project, selectedEnv } = useProjectContext();
  const { t } = useT();
  const envId = search.get("envId") || selectedEnv?.id || "";

  const [db, setDb] = useState<ResourceSnapshot | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

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
  }, [projectId, name, envId, selectedEnv]);

  if (isLoading) {
    return <div className="flex h-64 items-center justify-center"><Spinner size="lg" /></div>;
  }
  if (error || !db) {
    return (
      <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
        {error ?? t("databases.error.notFound")}
      </div>
    );
  }

  const summary = (db.summary_json ?? {}) as DbSummary;
  const spec = summary.spec ?? {};
  const dbName = summary.database ?? spec.database ?? db.name;
  const appRef = summary.app_ref ?? spec.appRef;
  const namespace = summary.namespace ?? spec.namespace;
  const backup = spec.backup;
  const backupOn = !!backup?.enabled;
  const host = namespace ? `${db.name}.${namespace}.svc.cluster.local` : db.name;

  return (
    <div>
      <div className="mb-8">
        <Breadcrumb
          items={[
            { label: t("common.crumb.projects"), href: "/projects" },
            { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
            { label: t("nav.databases"), href: `/projects/${projectId}/databases${envId ? `?env=${envId}` : ""}` },
            { label: db.name },
          ]}
        />
        <div className="mt-2 flex items-center gap-3">
          <h1 className="font-mono text-2xl font-bold text-gray-900">{db.name}</h1>
          <PhaseBadge phase={db.phase} />
        </div>
        <p className="mt-0.5 text-sm text-gray-500">{t("databases.detail.subtitle")}</p>
      </div>

      <section className="mb-8">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">{t("databases.detail.overview")}</h2>
        <div className="grid gap-4 rounded-xl border border-gray-200 bg-white p-6 shadow-sm sm:grid-cols-2 lg:grid-cols-4">
          <Field label={t("databases.detail.field.database")}>{dbName}</Field>
          <Field label={t("databases.detail.field.attachedApp")}>{appRef ? <span className="font-mono">{appRef}</span> : "—"}</Field>
          <Field label={t("databases.detail.field.environment")}>{selectedEnv?.name ?? "—"}</Field>
          <Field label={t("databases.detail.field.status")}>{db.phase || t("databases.detail.field.statusUnknown")}</Field>
        </div>
      </section>

      <section className="mb-8">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">{t("databases.detail.connection")}</h2>
        <div className="space-y-4 rounded-xl border border-gray-200 bg-white p-6 shadow-sm">
          <div className="flex items-center justify-between gap-3">
            <Field label={t("databases.detail.field.host")}><span className="font-mono text-xs sm:text-sm">{host}</span></Field>
            <CopyButton value={host} />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t("databases.detail.field.dbName")}><span className="font-mono">{dbName}</span></Field>
            <Field label={t("databases.detail.field.port")}><span className="font-mono">5432</span></Field>
          </div>
          <p className="rounded-lg bg-gray-50 px-3 py-2 text-xs text-gray-500">
            {t("databases.detail.credentials")}
          </p>
        </div>
      </section>

      <section>
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">{t("databases.detail.backups")}</h2>
        <div className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm">
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
            <div className="flex items-center gap-2 text-sm text-gray-500">
              <span className="h-2 w-2 rounded-full bg-gray-300" />
              {t("databases.detail.backup.disabled")}
            </div>
          )}
        </div>
      </section>
    </div>
  );
}

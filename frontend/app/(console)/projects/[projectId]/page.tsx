"use client";
import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { projectsApi, appsApi, databasesApi, customDomainsApi } from "@/lib/api";
import type { Project, Environment } from "@/lib/types";
import { StateChip } from "@/components/ui/state-chip";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { ResourceIcon } from "@/components/shell/icons";
import type { IconName } from "@/lib/resources";
import { useT } from "@/lib/i18n/console/context";
import { CostCard } from "@/components/cost/cost-card";
import { TemplateDeployCards } from "@/components/console/template-deploy-cards";
import { UploadDeployCard } from "@/components/deploy/upload-deploy";

type Counts = { apps: number; appsReady: number; dbs: number; domainsVerified: number; domainsPending: number };

export default function ProjectOverviewPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const { t } = useT();

  const [project, setProject] = useState<Project | null>(null);
  const [counts, setCounts] = useState<Counts | null>(null);
  const [envId, setEnvId] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const detail = await projectsApi.get(projectId);
        if (cancelled) return;
        setProject(detail.project);

        const envs: Environment[] = detail.environments ?? [];
        const prod = envs.find((e) => e.type === "prod") ?? envs[0];
        setEnvId(prod?.id ?? null);

        const [apps, dbs, domains] = await Promise.all([
          prod ? appsApi.list(projectId, prod.id).then((r) => r.apps).catch(() => []) : Promise.resolve([]),
          prod ? databasesApi.list(projectId, prod.id).then((r) => r.databases).catch(() => []) : Promise.resolve([]),
          customDomainsApi.listAuthorizations(projectId).then((r) => r.authorizations).catch(() => []),
        ]);
        if (cancelled) return;
        setCounts({
          apps: apps.length,
          appsReady: apps.filter((a) => a.phase === "Ready").length,
          dbs: dbs.length,
          domainsVerified: domains.filter((d) => d.status === "verified").length,
          domainsPending: domains.filter((d) => d.status !== "verified").length,
        });
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : t("overview.error.load"));
      } finally {
        if (!cancelled) setIsLoading(false);
      }
    }
    load();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId]);


  if (isLoading) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner size="lg" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
        {error}
      </div>
    );
  }

  if (!project) return null;

  const c = counts;
  const checklist = [
    { key: "deploy", label: t("overview.checklist.deploy"), href: `/projects/${projectId}/git/import`, done: !!c && c.apps > 0 },
    { key: "db", label: t("overview.checklist.db"), href: `/projects/${projectId}/databases`, done: !!c && c.dbs > 0 },
    { key: "domain", label: t("overview.checklist.domain"), href: `/projects/${projectId}/domains`, done: !!c && c.domainsVerified > 0 },
  ];
  const checklistDone = checklist.filter((i) => i.done).length;
  const checklistComplete = checklistDone === checklist.length;
  const showTemplates = !c || c.apps === 0;

  return (
    <div>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <Breadcrumb items={[{ label: t("common.crumb.projects"), href: "/projects" }, { label: project.display_name }]} />
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{project.display_name}</h1>
          <p className="mt-0.5 font-mono text-sm text-gray-400 dark:text-gray-500">{project.name}</p>
        </div>
        <Link
          href={`/projects/${projectId}/git/import`}
          className="inline-flex shrink-0 items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700"
        >
          <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
          </svg>
          {t("overview.deployApp")}
        </Link>
      </div>

      {c && (
        <div className="mb-8 flex flex-wrap items-center gap-2">
          {c.apps > 0 ? (
            <StateChip tone={c.appsReady === c.apps ? "ready" : "needs-action"} dot>
              {c.appsReady === c.apps ? t("overview.chip.ready") : t("overview.chip.needsAction")}
            </StateChip>
          ) : (
            <StateChip tone="needs-action" dot>{t("overview.chip.noApps")}</StateChip>
          )}
          <StateChip tone="neutral">{t("overview.chip.apps", { count: c.apps })}</StateChip>
          <StateChip tone={c.dbs > 0 ? "backup" : "neutral"}>{t("overview.chip.dbs", { count: c.dbs })}</StateChip>
          {c.domainsVerified > 0 ? (
            <StateChip tone="ready">{t("overview.chip.domainVerified", { count: c.domainsVerified })}</StateChip>
          ) : c.domainsPending > 0 ? (
            <StateChip tone="needs-action">{t("overview.chip.domainDns")}</StateChip>
          ) : (
            <StateChip tone="neutral">{t("overview.chip.domainNone")}</StateChip>
          )}
        </div>
      )}

      <CostCard projectId={projectId} />

      {showTemplates && (
        <div data-onboarding="first-deploy" className="mb-8 grid gap-4 lg:grid-cols-2">
          <TemplateDeployCards projectId={projectId} envId={envId} hero />
          <UploadDeployCard projectId={projectId} envId={envId} hero />
        </div>
      )}

      {!checklistComplete && (
        <div className="mb-8 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
          <div className="mb-3 flex items-center justify-between">
            <h2 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("overview.checklist.title")}</h2>
            <span className="text-xs text-gray-400 dark:text-gray-500">{t("overview.checklist.progress", { done: checklistDone, total: checklist.length })}</span>
          </div>
          <div className="mb-4 h-1.5 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
            <div
              className="h-full rounded-full bg-blue-600 transition-all"
              style={{ width: `${(checklistDone / checklist.length) * 100}%` }}
            />
          </div>
          <ul className="space-y-1">
            {checklist.map((item) => (
              <li key={item.key}>
                <Link
                  href={item.href}
                  className="group flex items-center gap-3 rounded-lg px-2 py-2 transition-colors hover:bg-gray-50 dark:hover:bg-gray-800"
                >
                  <span
                    className={`flex h-5 w-5 shrink-0 items-center justify-center rounded-full border ${
                      item.done ? "border-green-500 bg-green-500 text-white" : "border-gray-300 dark:border-gray-700 text-transparent"
                    }`}
                  >
                    <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={3} d="M5 13l4 4L19 7" />
                    </svg>
                  </span>
                  <span className={`flex-1 text-sm ${item.done ? "text-gray-400 dark:text-gray-500 line-through" : "text-gray-700 dark:text-gray-200"}`}>
                    {item.label}
                  </span>
                  {!item.done && (
                    <span className="text-xs text-blue-600 opacity-0 transition-opacity group-hover:opacity-100">
                      {t("overview.checklist.go")}
                    </span>
                  )}
                </Link>
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="mb-8 grid gap-4 sm:grid-cols-3">
        <ActionCard
          icon="apps"
          tone="blue"
          title={t("overview.card.app.title")}
          hint={t("overview.card.app.hint")}
          cta={t("overview.card.app.cta")}
          href={`/projects/${projectId}/git/import`}
        />
        <ActionCard
          icon="databases"
          tone="green"
          title={t("overview.card.db.title")}
          hint={t("overview.card.db.hint")}
          cta={t("overview.card.db.cta")}
          href={`/projects/${projectId}/databases`}
        />
        <ActionCard
          icon="domains"
          tone="indigo"
          title={t("overview.card.domain.title")}
          hint={t("overview.card.domain.hint")}
          cta={t("overview.card.domain.cta")}
          href={`/projects/${projectId}/domains`}
        />
      </div>

      <div className="mb-8">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("overview.section.more")}</h2>
        <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
          <SecondaryLink icon="ai" label={t("nav.ai")} hint={t("overview.secondary.ai.hint")} href={`/projects/${projectId}/ai`} />
          <SecondaryLink icon="monitoring" label={t("nav.monitoring")} hint={t("overview.secondary.monitoring.hint")} href={`/projects/${projectId}/monitoring`} />
          <SecondaryLink icon="storage" label={t("nav.storage")} hint={t("overview.secondary.storage.hint")} href={`/projects/${projectId}/storage`} />
          <SecondaryLink icon="models" label={t("nav.models")} hint={t("overview.secondary.models.hint")} href={`/projects/${projectId}/models`} />
          <SecondaryLink icon="app-servers" label={t("nav.app-servers")} hint={t("overview.secondary.appServers.hint")} href={`/projects/${projectId}/app-servers`} />
          <SecondaryLink icon="git" label={t("nav.git")} hint={t("overview.secondary.git.hint")} href={`/projects/${projectId}/git`} />
          <SecondaryLink icon="billing" label={t("nav.billing")} hint={t("overview.secondary.billing.hint")} href={`/projects/${projectId}/billing`} />
        </div>
      </div>

    </div>
  );
}

const toneClasses: Record<string, string> = {
  blue: "bg-blue-100 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400",
  green: "bg-green-100 dark:bg-green-950/40 text-green-600 dark:text-green-400",
  indigo: "bg-indigo-100 dark:bg-indigo-950/40 text-indigo-600 dark:text-indigo-400",
};

function ActionCard({
  icon,
  tone,
  title,
  hint,
  cta,
  href,
}: {
  icon: IconName;
  tone: keyof typeof toneClasses;
  title: string;
  hint: string;
  cta: string;
  href: string;
}) {
  return (
    <div className="flex flex-col rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
      <div className={`mb-4 flex h-10 w-10 items-center justify-center rounded-lg ${toneClasses[tone]}`}>
        <ResourceIcon name={icon} className="h-5 w-5" />
      </div>
      <p className="text-sm font-semibold text-gray-900 dark:text-gray-100">{title}</p>
      <p className="mt-1 flex-1 text-sm text-gray-500 dark:text-gray-400">{hint}</p>
      <Link
        href={href}
        className="mt-4 inline-flex items-center justify-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700"
      >
        {cta}
      </Link>
    </div>
  );
}

function SecondaryLink({
  icon,
  label,
  hint,
  href,
}: {
  icon: IconName;
  label: string;
  hint: string;
  href: string;
}) {
  return (
    <Link
      href={href}
      className="group flex items-center gap-3 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-4 py-3 shadow-sm transition-all hover:border-blue-200 hover:shadow-md"
    >
      <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400 transition-colors group-hover:bg-blue-600 group-hover:text-white">
        <ResourceIcon name={icon} className="h-4 w-4" />
      </div>
      <div className="min-w-0">
        <p className="text-sm font-medium text-gray-900 dark:text-gray-100">{label}</p>
        <p className="truncate text-xs text-gray-400 dark:text-gray-500">{hint}</p>
      </div>
    </Link>
  );
}

"use client";
import { useEffect, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { projectsApi, appsApi, databasesApi, customDomainsApi } from "@/lib/api";
import { useProjectContext } from "@/lib/project-context";
import { isAdmin } from "@/lib/rbac";
import { DeleteImpactModal, deleteImpactTargetKey, type DeleteImpactTarget } from "@/components/resources/delete-impact-modal";
import type { Project, Environment, ResourceSnapshot } from "@/lib/types";
import { StateChip } from "@/components/ui/state-chip";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { ResourceIcon } from "@/components/shell/icons";
import type { IconName } from "@/lib/resources";
import { useT } from "@/lib/i18n/console/context";
import { CostCard } from "@/components/cost/cost-card";
import { EmptyProjectOnramp } from "@/components/console/empty-project-onramp";
import { ProjectAppHealthList } from "@/components/console/project-app-health-list";
import { ProjectBoxesPanel } from "@/components/console/project-boxes-panel";

type Counts = { apps: number; appsReady: number; dbs: number; domainsVerified: number; domainsPending: number };

/**
 * Picks the environment this page summarises.
 *
 * `type` alone cannot answer that. A torn-down PR preview can come back as a
 * type=prod row (incident 2026-08-03), and "pr-6-fonbet-value" sorts ahead of
 * "prod", so `find(e => e.type === "prod")` landed on an empty preview and the
 * page reported "no apps" for a project serving live traffic. The project's own
 * `default_environment` is the only authoritative answer; type and list order
 * stay as fallbacks for projects that never set one.
 */
function pickDefaultEnv(envs: Environment[], defaultEnvironment?: string): Environment | undefined {
  return (
    (defaultEnvironment ? envs.find((e) => e.name === defaultEnvironment) : undefined) ??
    envs.find((e) => e.type === "prod") ??
    envs[0]
  );
}

export default function ProjectOverviewPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const router = useRouter();
  const { role, refetchProjects } = useProjectContext();
  const { t } = useT();

  const [project, setProject] = useState<Project | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<DeleteImpactTarget | null>(null);
  const [counts, setCounts] = useState<Counts | null>(null);
  const [projectApps, setProjectApps] = useState<ResourceSnapshot[]>([]);
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
        const prod = pickDefaultEnv(envs, detail.project.default_environment);
        setEnvId(prod?.id ?? null);

        const [apps, dbs, domains] = await Promise.all([
          prod ? appsApi.list(projectId, prod.id).then((r) => r.apps).catch(() => []) : Promise.resolve([]),
          prod ? databasesApi.list(projectId, prod.id).then((r) => r.databases).catch(() => []) : Promise.resolve([]),
          customDomainsApi.listAuthorizations(projectId).then((r) => r.authorizations).catch(() => []),
        ]);
        if (cancelled) return;
        setProjectApps(apps);
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
  const isEmpty = !c || (c.apps === 0 && c.dbs === 0);

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

      {c && !isEmpty && (
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

      {projectApps.length > 0 && <ProjectAppHealthList apps={projectApps} projectId={projectId} />}

      {!isEmpty && <CostCard projectId={projectId} />}

      {isEmpty && <EmptyProjectOnramp projectId={projectId} envId={envId} />}

      {!isEmpty && !checklistComplete && (
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

      <ProjectBoxesPanel projectId={projectId} />

      {!isEmpty && (
        <div className="mb-8 flex flex-wrap items-center gap-x-5 gap-y-2 border-t border-gray-200 pt-4 dark:border-gray-800">
          <SecondaryLink icon="monitoring" label={t("nav.monitoring")} href={`/projects/${projectId}/monitoring`} />
          <SecondaryLink icon="git" label={t("nav.git")} href={`/projects/${projectId}/git`} />
          <SecondaryLink icon="billing" label={t("nav.billing")} href={`/projects/${projectId}/billing`} />
        </div>
      )}

      {isAdmin(role) && (
        <div className="mt-10 rounded-xl border border-red-200 dark:border-red-900 bg-white dark:bg-gray-900 px-5 py-5">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h2 className="text-sm font-semibold text-red-700 dark:text-red-400">{t("overview.dangerZone.title")}</h2>
              <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("overview.dangerZone.subtitle")}</p>
            </div>
            <button
              onClick={() => setDeleteTarget({ kind: "project", projectId, projectName: project.name })}
              className="inline-flex items-center gap-2 rounded-lg border border-red-200 dark:border-red-900 bg-white dark:bg-gray-900 px-3 py-2 text-sm font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/30 transition-colors shadow-sm"
            >
              {t("overview.dangerZone.delete")}
            </button>
          </div>
        </div>
      )}

      {deleteTarget && (
        <DeleteImpactModal
          key={deleteImpactTargetKey(deleteTarget)}
          target={deleteTarget}
          onClose={() => setDeleteTarget(null)}
          onDeleted={() => {
            setDeleteTarget(null);
            refetchProjects();
            router.push("/projects");
          }}
        />
      )}
    </div>
  );
}

/**
 * Compact link to a surface that holds no sidebar slot (monitoring, builds,
 * billing). Deliberately a text link and not a card: the overview used to
 * repeat every destination as a bordered CTA card, which restated the sidebar
 * and buried the one action that matters on an empty project.
 */
function SecondaryLink({
  icon,
  label,
  href,
}: {
  icon: IconName;
  label: string;
  href: string;
}) {
  return (
    <Link
      href={href}
      className="inline-flex items-center gap-2 text-sm text-gray-500 transition-colors hover:text-blue-600 dark:text-gray-400 dark:hover:text-blue-400"
    >
      <ResourceIcon name={icon} className="h-4 w-4" />
      {label}
    </Link>
  );
}

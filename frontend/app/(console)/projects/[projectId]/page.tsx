"use client";
import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import {
  projectsApi,
  appsApi,
  databasesApi,
  customDomainsApi,
} from "@/lib/api";
import type { Project, Environment, Operation } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { StateChip } from "@/components/ui/state-chip";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { EmptyState } from "@/components/ui/empty-state";
import { ResourceIcon } from "@/components/shell/icons";
import type { IconName } from "@/lib/resources";
import { timeAgo } from "@/lib/format";

// Task-oriented project overview ("action dashboard"). Instead of a catalog of
// infra entities, the first screen drives the user toward first value:
// deploy an app → add a database → add a domain, with an onboarding checklist
// and at-a-glance status. See the console redesign spec.

type Counts = { apps: number; appsReady: number; dbs: number; domainsVerified: number; domainsPending: number };

export default function ProjectOverviewPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;

  const [project, setProject] = useState<Project | null>(null);
  const [operations, setOperations] = useState<Operation[]>([]);
  const [counts, setCounts] = useState<Counts | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const [detail, ops] = await Promise.all([
          projectsApi.get(projectId),
          projectsApi.operations(projectId),
        ]);
        if (cancelled) return;
        setProject(detail.project);
        setOperations((ops.operations ?? []).slice(0, 5));

        const envs: Environment[] = detail.environments ?? [];
        const prod = envs.find((e) => e.type === "prod") ?? envs[0];

        // Resource counts power the status chips + onboarding checklist. Each
        // call is best-effort: a single failing surface must not blank the
        // whole overview, so we default to zero on error.
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
        if (!cancelled) setError(err instanceof Error ? err.message : "Не удалось загрузить проект");
      } finally {
        if (!cancelled) setIsLoading(false);
      }
    }
    load();
    return () => {
      cancelled = true;
    };
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
      <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
        {error}
      </div>
    );
  }

  if (!project) return null;

  const c = counts;
  const checklist = [
    { key: "deploy", label: "Разверните приложение из GitHub", href: `/projects/${projectId}/git/import`, done: !!c && c.apps > 0 },
    { key: "db", label: "Добавьте базу данных Postgres", href: `/projects/${projectId}/databases`, done: !!c && c.dbs > 0 },
    { key: "domain", label: "Подключите домен и HTTPS", href: `/projects/${projectId}/domains`, done: !!c && c.domainsVerified > 0 },
  ];
  const checklistDone = checklist.filter((i) => i.done).length;
  const checklistComplete = checklistDone === checklist.length;

  return (
    <div>
      {/* Header */}
      <div className="mb-6 flex items-start justify-between gap-4">
        <div className="min-w-0">
          <Breadcrumb items={[{ label: "Проекты", href: "/projects" }, { label: project.display_name }]} />
          <h1 className="mt-2 text-2xl font-bold text-gray-900">{project.display_name}</h1>
          <p className="mt-0.5 font-mono text-sm text-gray-400">{project.name}</p>
        </div>
        <Link
          href={`/projects/${projectId}/git/import`}
          className="inline-flex shrink-0 items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700"
        >
          <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
          </svg>
          Развернуть приложение
        </Link>
      </div>

      {/* Status chips */}
      {c && (
        <div className="mb-8 flex flex-wrap items-center gap-2">
          {c.apps > 0 ? (
            <StateChip tone={c.appsReady === c.apps ? "ready" : "needs-action"} dot>
              prod · {c.appsReady === c.apps ? "работает" : "требует внимания"}
            </StateChip>
          ) : (
            <StateChip tone="needs-action" dot>prod · нет приложений</StateChip>
          )}
          <StateChip tone="neutral">{c.apps} прил.</StateChip>
          <StateChip tone={c.dbs > 0 ? "backup" : "neutral"}>{c.dbs} БД</StateChip>
          {c.domainsVerified > 0 ? (
            <StateChip tone="ready">{c.domainsVerified} домен</StateChip>
          ) : c.domainsPending > 0 ? (
            <StateChip tone="needs-action">домен: проверка DNS</StateChip>
          ) : (
            <StateChip tone="neutral">домен не подключён</StateChip>
          )}
        </div>
      )}

      {/* Onboarding checklist — hidden once everything is done */}
      {!checklistComplete && (
        <div className="mb-8 rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
          <div className="mb-3 flex items-center justify-between">
            <h2 className="text-sm font-semibold text-gray-900">Запуск проекта</h2>
            <span className="text-xs text-gray-400">{checklistDone} из {checklist.length}</span>
          </div>
          <div className="mb-4 h-1.5 w-full overflow-hidden rounded-full bg-gray-100">
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
                  className="group flex items-center gap-3 rounded-lg px-2 py-2 transition-colors hover:bg-gray-50"
                >
                  <span
                    className={`flex h-5 w-5 shrink-0 items-center justify-center rounded-full border ${
                      item.done ? "border-green-500 bg-green-500 text-white" : "border-gray-300 text-transparent"
                    }`}
                  >
                    <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={3} d="M5 13l4 4L19 7" />
                    </svg>
                  </span>
                  <span className={`flex-1 text-sm ${item.done ? "text-gray-400 line-through" : "text-gray-700"}`}>
                    {item.label}
                  </span>
                  {!item.done && (
                    <span className="text-xs text-blue-600 opacity-0 transition-opacity group-hover:opacity-100">
                      Перейти →
                    </span>
                  )}
                </Link>
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* Primary action cards */}
      <div className="mb-8 grid gap-4 sm:grid-cols-3">
        <ActionCard
          icon="apps"
          tone="blue"
          title="Развернуть приложение"
          hint="Подключите репозиторий GitHub — сборка и деплой без YAML."
          cta="Подключить GitHub"
          href={`/projects/${projectId}/git/import`}
        />
        <ActionCard
          icon="databases"
          tone="green"
          title="Добавить базу данных"
          hint="Создайте Postgres и подключите строку соединения к приложению."
          cta="Создать Postgres"
          href={`/projects/${projectId}/databases`}
        />
        <ActionCard
          icon="domains"
          tone="indigo"
          title="Добавить домен"
          hint="Свой домен с автоматическим выпуском HTTPS-сертификата."
          cta="Добавить домен"
          href={`/projects/${projectId}/domains`}
        />
      </div>

      {/* Secondary resources */}
      <div className="mb-8">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">Ещё в проекте</h2>
        <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
          <SecondaryLink icon="monitoring" label="Мониторинг" hint="Логи и метрики" href={`/projects/${projectId}/monitoring`} />
          <SecondaryLink icon="operations" label="Операции" hint="История деплоев" href={`/projects/${projectId}/operations`} />
          <SecondaryLink icon="storage" label="Объектное хранилище" hint="S3-совместимые бакеты" href={`/projects/${projectId}/storage`} />
          <SecondaryLink icon="models" label="AI-модели" hint="Инференс KServe" href={`/projects/${projectId}/models`} />
          <SecondaryLink icon="app-servers" label="Серверы приложений" hint="VM-хосты" href={`/projects/${projectId}/app-servers`} />
          <SecondaryLink icon="git" label="Сборки" hint="Репозитории и билды" href={`/projects/${projectId}/git`} />
        </div>
      </div>

      {/* Recent operations */}
      <div>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-500">Последние операции</h2>
          <Link href={`/projects/${projectId}/operations`} className="text-xs text-blue-600 hover:text-blue-700">
            Все операции →
          </Link>
        </div>

        {operations.length === 0 ? (
          <EmptyState
            title="Пока нет операций"
            description="Деплои, создание баз и доменов появятся здесь после первого действия."
            action={{ label: "Развернуть приложение", href: `/projects/${projectId}/git/import` }}
          />
        ) : (
          <div className="overflow-hidden rounded-xl border border-gray-200 bg-white">
            {operations.map((op, idx) => (
              <div
                key={op.id}
                className={`flex items-center gap-4 px-5 py-4 ${
                  idx < operations.length - 1 ? "border-b border-gray-100" : ""
                }`}
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium text-gray-900">{op.action}</span>
                    <span className="text-xs text-gray-400">·</span>
                    <span className="font-mono text-xs text-gray-500">{op.resource_name}</span>
                  </div>
                  <div className="mt-0.5 text-xs text-gray-400">{timeAgo(op.created_at)}</div>
                </div>
                <Badge status={op.status} />
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

const toneClasses: Record<string, string> = {
  blue: "bg-blue-100 text-blue-600",
  green: "bg-green-100 text-green-600",
  indigo: "bg-indigo-100 text-indigo-600",
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
    <div className="flex flex-col rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
      <div className={`mb-4 flex h-10 w-10 items-center justify-center rounded-lg ${toneClasses[tone]}`}>
        <ResourceIcon name={icon} className="h-5 w-5" />
      </div>
      <p className="text-sm font-semibold text-gray-900">{title}</p>
      <p className="mt-1 flex-1 text-sm text-gray-500">{hint}</p>
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
      className="group flex items-center gap-3 rounded-lg border border-gray-200 bg-white px-4 py-3 shadow-sm transition-all hover:border-blue-200 hover:shadow-md"
    >
      <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-gray-100 text-gray-500 transition-colors group-hover:bg-blue-600 group-hover:text-white">
        <ResourceIcon name={icon} className="h-4 w-4" />
      </div>
      <div className="min-w-0">
        <p className="text-sm font-medium text-gray-900">{label}</p>
        <p className="truncate text-xs text-gray-400">{hint}</p>
      </div>
    </Link>
  );
}

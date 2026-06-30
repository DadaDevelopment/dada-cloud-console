"use client";
import { useState } from "react";
import { useParams, useSearchParams } from "next/navigation";
import Link from "next/link";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { EnvVarsEditor } from "@/components/deploy/env-vars-editor";
import { HostnamesManager } from "@/components/deploy/hostnames-manager";
import { useT } from "@/lib/i18n/console/context";

type Tab = "env" | "git" | "domains";

export default function AppSettingsPage() {
  const params = useParams<{ projectId: string; appName: string }>();
  const { projectId, appName } = params;
  const searchParams = useSearchParams();
  const { selectedEnv, role } = useProjectContext();
  const envId = searchParams.get("envId") || selectedEnv?.id || "";
  const { t } = useT();

  const [tab, setTab] = useState<Tab>("env");
  const canEdit = canMutate(role);

  const tabs: { key: Tab; label: string }[] = [
    { key: "env", label: t("apps.settings.tab.env") },
    { key: "git", label: t("apps.settings.tab.git") },
    { key: "domains", label: t("apps.settings.tab.domains") },
  ];

  return (
    <div>
      <Breadcrumb
        items={[
          { label: t("common.crumb.projects"), href: "/projects" },
          { label: t("common.crumb.overview"), href: `/projects/${projectId}` },
          { label: t("nav.apps"), href: `/projects/${projectId}/apps` },
          { label: appName, href: `/projects/${projectId}/apps/${appName}${envId ? `?envId=${envId}` : ""}` },
          { label: t("apps.settings.crumb") },
        ]}
      />
      <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">
        <span className="font-mono">{appName}</span>
        <span className="ml-2 text-lg font-normal text-gray-400 dark:text-gray-500">{t("apps.settings.heading.suffix")}</span>
      </h1>

      {/* Tabs */}
      <div className="mb-6 mt-4 border-b border-gray-200 dark:border-gray-800">
        <nav className="-mb-px flex gap-6">
          {tabs.map((t) => (
            <button
              key={t.key}
              onClick={() => setTab(t.key)}
              className={`border-b-2 pb-3 text-sm font-medium transition-colors ${
                tab === t.key
                  ? "border-blue-600 text-blue-600 dark:text-blue-400"
                  : "border-transparent text-gray-500 dark:text-gray-400 hover:border-gray-300 dark:hover:border-gray-700 hover:text-gray-700 dark:hover:text-gray-200"
              }`}
            >
              {t.label}
            </button>
          ))}
        </nav>
      </div>

      {tab === "env" && (
        <EnvVarsEditor projectId={projectId} envId={envId} appName={appName} canEdit={canEdit} />
      )}

      {tab === "git" && (
        <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-6">
          <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("apps.settings.git.title")}</h2>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {t("apps.settings.git.subtitle")}
          </p>
          <div className="mt-4 flex gap-3">
            <Link
              href={`/projects/${projectId}/git${envId ? `?envId=${envId}` : ""}`}
              className="rounded-lg border border-gray-300 dark:border-gray-700 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-50"
            >
              {t("apps.settings.git.manageRepos")}
            </Link>
            <Link
              href={`/projects/${projectId}/apps/${appName}/deployments${envId ? `?envId=${envId}` : ""}`}
              className="rounded-lg border border-gray-300 dark:border-gray-700 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-50"
            >
              {t("apps.settings.git.viewDeployments")}
            </Link>
          </div>
        </div>
      )}

      {tab === "domains" && (
        <HostnamesManager projectId={projectId} envId={envId} appName={appName} canEdit={canEdit} />
      )}
    </div>
  );
}

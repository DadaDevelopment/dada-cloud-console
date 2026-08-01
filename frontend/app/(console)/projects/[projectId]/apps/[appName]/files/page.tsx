"use client";
import { useParams, useSearchParams } from "next/navigation";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { Spinner } from "@/components/ui/spinner";
import { FileBrowser } from "@/components/files/file-browser";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { useT } from "@/lib/i18n/console/context";

export default function AppFilesPage() {
  const params = useParams<{ projectId: string; appName: string }>();
  const searchParams = useSearchParams();
  const { role, loading: roleLoading, selectedEnv } = useProjectContext();
  const { projectId, appName } = params;
  const envId = searchParams.get("envId") || selectedEnv?.id || "";
  const initialPath = searchParams.get("path") || "/";
  const { t } = useT();

  if (roleLoading || !envId) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner size="lg" />
      </div>
    );
  }

  return (
    <div>
      <div className="mb-6">
        <Breadcrumb
          items={[
            { label: t("common.crumb.projects"), href: "/projects" },
            { label: t("common.crumb.overview"), href: `/projects/${projectId}` },
            { label: t("nav.apps"), href: `/projects/${projectId}/apps` },
            { label: appName, href: `/projects/${projectId}/apps/${appName}?envId=${envId}` },
            { label: t("apps.files.crumb") },
          ]}
        />
        <div className="mt-2 flex flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">
              <span className="font-mono">{appName}</span>
              <span className="ml-2 text-lg font-normal text-gray-400 dark:text-gray-500">
                {t("apps.files.title")}
              </span>
            </h1>
            <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("apps.files.subtitle")}</p>
          </div>
          <a
            href={`/projects/${projectId}/apps/${appName}/settings?envId=${envId}`}
            className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800"
          >
            {t("apps.files.settingsLink")}
          </a>
        </div>
      </div>

      <FileBrowser
        projectId={projectId}
        envId={envId}
        appName={appName}
        canWrite={canMutate(role)}
        initialPath={initialPath}
      />
    </div>
  );
}

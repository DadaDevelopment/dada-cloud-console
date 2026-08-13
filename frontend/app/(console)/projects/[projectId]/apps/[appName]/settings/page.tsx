"use client";
import { useEffect, useRef, useState } from "react";
import { useParams, useSearchParams } from "next/navigation";
import Link from "next/link";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { customDomainsApi, appsApi, gitApi } from "@/lib/api";
import type { DomainAuthorization } from "@/lib/types";
import { trackUxEvent } from "@/lib/ux-telemetry";
import { EnvVarsEditor } from "@/components/deploy/env-vars-editor";
import { HostnamesManager } from "@/components/deploy/hostnames-manager";
import { StorageManager } from "@/components/deploy/storage-manager";
import { ResourceManager } from "@/components/deploy/resource-manager";
import { PaymentsManager } from "@/components/payments/payments-manager";
import { CommonConfigEditor } from "@/components/deploy/common-config-editor";
import { StartCommandEditor } from "@/components/deploy/start-command-editor";
import { ComposeConfigEditor } from "@/components/deploy/compose-config-editor";
import { ComposeVolumeEditor } from "@/components/deploy/compose-volume-editor";
import { ArchiveReuploadControl } from "@/components/deploy/archive-reupload";
import { useT } from "@/lib/i18n/console/context";

type Tab = "env" | "config" | "git" | "domains" | "storage" | "resources" | "payments";

export default function AppSettingsPage() {
  const params = useParams<{ projectId: string; appName: string }>();
  const { projectId, appName } = params;
  const searchParams = useSearchParams();
  const { environments, selectedEnv, role } = useProjectContext();
  const envId = searchParams.get("envId") || selectedEnv?.id || "";
  const activeEnv = environments.find((e) => e.id === envId) ?? selectedEnv;
  const isVM = activeEnv?.runtime === "vm";
  const { t } = useT();

  const initialTab = ((): Tab => {
    const q = searchParams.get("tab");
    return q === "config" || q === "git" || q === "domains" || q === "storage" || q === "resources" || q === "payments"
      ? q
      : "env";
  })();
  const [tab, setTab] = useState<Tab>(initialTab);
  const [verifiedApexes, setVerifiedApexes] = useState<DomainAuthorization[]>([]);
  const [isUploadedSource, setIsUploadedSource] = useState(false);
  const [anonymousAccessRepo, setAnonymousAccessRepo] = useState(false);
  const [platformAccess, setPlatformAccess] = useState<string | null>(null);
  const [sourceDownloadBusy, setSourceDownloadBusy] = useState(false);
  const [sourceDownloadError, setSourceDownloadError] = useState<string | null>(null);
  const canEdit = canMutate(role);
  const gitTabViewedRef = useRef<string | null>(null);

  useEffect(() => {
    customDomainsApi
      .listAuthorizations(projectId)
      .then((d) => setVerifiedApexes((d.authorizations ?? []).filter((a) => a.status === "verified")))
      .catch(() => setVerifiedApexes([]));
  }, [projectId]);

  useEffect(() => {
    if (!envId) return;
    gitApi
      .listRepos(projectId, envId)
      .then((d) => {
        const repo = (d.repos ?? []).find((r) => r.app_name === appName);
        setIsUploadedSource(repo?.provider === "archive");
        setAnonymousAccessRepo(repo?.platform_access === "anonymous");
        setPlatformAccess(repo ? repo.platform_access || "unknown" : "none");
      })
      .catch(() => {
        setIsUploadedSource(false);
        setAnonymousAccessRepo(false);
        setPlatformAccess("load_failed");
      });
  }, [projectId, envId, appName]);

  useEffect(() => {
    if (tab !== "git" || platformAccess === null) return;
    if (gitTabViewedRef.current === platformAccess) return;
    gitTabViewedRef.current = platformAccess;
    trackUxEvent("view", `app_git_tab:${platformAccess}`);
    if (platformAccess === "anonymous") {
      trackUxEvent("view", "git_platform_access_cta:panel");
    }
  }, [tab, platformAccess]);

  async function downloadSource() {
    setSourceDownloadBusy(true);
    setSourceDownloadError(null);
    try {
      const d = await appsApi.downloadSourceArchive(projectId, envId, appName);
      window.location.href = d.url;
    } catch (e) {
      setSourceDownloadError(e instanceof Error ? e.message : t("apps.settings.source.error"));
    } finally {
      setSourceDownloadBusy(false);
    }
  }

  const validTabsForVM: Tab[] = ["env", "config", "storage", "payments", "domains", "git"];
  const effectiveTab: Tab = isVM && !validTabsForVM.includes(tab) ? "env" : tab;

  const tabs: { key: Tab; label: string }[] = isVM
    ? [
        { key: "env", label: t("apps.settings.tab.env") },
        { key: "config", label: t("apps.settings.tab.config") },
        { key: "storage", label: t("apps.settings.tab.storage") },
        { key: "payments", label: t("apps.settings.tab.payments") },
        { key: "domains", label: t("apps.settings.tab.domains") },
        { key: "git", label: t("apps.settings.tab.git") },
      ]
    : [
        { key: "env", label: t("apps.settings.tab.env") },
        { key: "config", label: t("apps.settings.tab.config") },
        { key: "git", label: t("apps.settings.tab.git") },
        { key: "storage", label: t("apps.settings.tab.storage") },
        { key: "resources", label: t("apps.settings.tab.resources") },
        { key: "payments", label: t("apps.settings.tab.payments") },
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
                effectiveTab === t.key
                  ? "border-blue-600 text-blue-600 dark:text-blue-400"
                  : "border-transparent text-gray-500 dark:text-gray-400 hover:border-gray-300 dark:hover:border-gray-700 hover:text-gray-700 dark:hover:text-gray-200"
              }`}
            >
              {t.label}
            </button>
          ))}
        </nav>
      </div>

      {effectiveTab === "env" && (
        <EnvVarsEditor projectId={projectId} envId={envId} appName={appName} canEdit={canEdit} />
      )}

      {effectiveTab === "git" && (
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

          {anonymousAccessRepo && (
            <div className="mt-5 rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-800 dark:text-amber-300">
              <p className="font-medium">{t("apps.settings.git.anonAccess.title")}</p>
              <p className="mt-1">{t("apps.settings.git.anonAccess.body")}</p>
              <Link
                href={`/projects/${projectId}/git${envId ? `?envId=${envId}` : ""}`}
                data-ux="git_platform_access_cta:reconnect_repo"
                className="mt-2 inline-block font-medium underline underline-offset-2"
              >
                {t("apps.settings.git.anonAccess.cta")}
              </Link>
            </div>
          )}

          {isUploadedSource && (
            <div className="mt-6 border-t border-gray-200 dark:border-gray-800 pt-5">
              <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">
                {t("apps.settings.source.title")}
              </h3>
              <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {t("apps.settings.source.subtitle")}
              </p>
              <button
                onClick={downloadSource}
                disabled={sourceDownloadBusy}
                className="mt-3 rounded-lg border border-gray-300 dark:border-gray-700 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-50 disabled:opacity-50"
              >
                {sourceDownloadBusy ? t("apps.settings.source.busy") : t("apps.settings.source.download")}
              </button>
              {sourceDownloadError && (
                <p className="mt-2 text-sm text-red-600 dark:text-red-400">{sourceDownloadError}</p>
              )}

              {canEdit && envId && (
                <ArchiveReuploadControl
                  projectId={projectId}
                  envId={envId}
                  appName={appName}
                  className="mt-5 border-t border-gray-200 dark:border-gray-800 pt-5"
                />
              )}
            </div>
          )}
        </div>
      )}

      {effectiveTab === "config" && (
        isVM ? (
          <ComposeConfigEditor projectId={projectId} envId={envId} appName={appName} canEdit={canEdit} />
        ) : (
          <div className="space-y-6">
            <CommonConfigEditor
              projectId={projectId}
              envId={envId}
              appName={appName}
              canEdit={canEdit}
              isUploadedSource={isUploadedSource}
            />
            <StartCommandEditor projectId={projectId} envId={envId} appName={appName} canEdit={canEdit} />
          </div>
        )
      )}

      {effectiveTab === "storage" && (
        isVM ? (
          <ComposeVolumeEditor projectId={projectId} envId={envId} appName={appName} canEdit={canEdit} />
        ) : (
          <StorageManager projectId={projectId} envId={envId} appName={appName} canEdit={canEdit} />
        )
      )}

      {effectiveTab === "resources" && !isVM && (
        <ResourceManager projectId={projectId} envId={envId} appName={appName} canEdit={canEdit} />
      )}

      {effectiveTab === "payments" && (
        <PaymentsManager projectId={projectId} envId={envId} appName={appName} canEdit={canEdit} />
      )}

      {effectiveTab === "domains" && (
        <HostnamesManager projectId={projectId} envId={envId} appName={appName} canEdit={canEdit} verifiedApexes={verifiedApexes} />
      )}
    </div>
  );
}

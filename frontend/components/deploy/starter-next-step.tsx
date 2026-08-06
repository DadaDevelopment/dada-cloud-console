"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { GitBranch, UploadCloud } from "lucide-react";
import { useT } from "@/lib/i18n/console/context";
import { trackUxEvent } from "@/lib/ux-telemetry";
import { isStarterRepo } from "@/lib/starter-templates";
import { UploadDeployCard } from "@/components/deploy/upload-deploy";

export interface StarterNextStepProps {
  projectId: string;
  envId: string | null;
  repoFullName?: string | null;
  className?: string;
}

/**
 * Shown once a starter-template app has a successful build: the demo works,
 * but the app is still running the sample repo, not the user's own code.
 * Live psql showed this is exactly where most starter-template users stop --
 * they trigger the demo build, watch it succeed, and never return. Offers
 * the two no-git-required-for-upload paths that already exist elsewhere in
 * the product: connect a real repo, or upload an archive right here (the
 * upload flow is embedded via `UploadDeployCard`, not reimplemented).
 *
 * Renders nothing when the app is not on a starter repo, so callers can
 * mount it unconditionally next to the build success state. An environment is
 * required too: both actions target a specific environment, and a link built
 * with an empty `envId` lands the user on an import page that cannot submit.
 */
export function StarterNextStep({ projectId, envId, repoFullName, className }: StarterNextStepProps) {
  const { t } = useT();
  const [uploadOpen, setUploadOpen] = useState(false);
  const viewedRef = useRef(false);

  const show = isStarterRepo(repoFullName) && Boolean(envId);

  useEffect(() => {
    if (!show || viewedRef.current) return;
    viewedRef.current = true;
    trackUxEvent("view", "starter_next_step:panel");
  }, [show]);

  if (!show) return null;

  return (
    <div
      className={`rounded-xl border border-blue-200 dark:border-blue-900/60 bg-blue-50/60 dark:bg-blue-950/20 p-5 shadow-sm ${className ?? ""}`}
    >
      <h2 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("starterNext.title")}</h2>
      <p className="mt-1 text-sm text-gray-600 dark:text-gray-300">{t("starterNext.subtitle")}</p>

      <div className="mt-4 grid gap-3 sm:grid-cols-2">
        <div className="rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4">
          <div className="mb-3 flex h-9 w-9 items-center justify-center rounded-lg bg-blue-100 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400">
            <GitBranch className="h-4 w-4" />
          </div>
          <p className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("starterNext.connectGit.title")}</p>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("starterNext.connectGit.hint")}</p>
          <Link
            href={`/projects/${projectId}/git/import?envId=${encodeURIComponent(envId ?? "")}`}
            data-ux="starter_next_step:connect_git"
            onClick={() => trackUxEvent("click", "starter_next_step:connect_git")}
            className="mt-4 inline-flex items-center justify-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-blue-700"
          >
            {t("starterNext.connectGit.cta")}
          </Link>
        </div>

        <div className="rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4">
          <div className="mb-3 flex h-9 w-9 items-center justify-center rounded-lg bg-blue-100 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400">
            <UploadCloud className="h-4 w-4" />
          </div>
          <p className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("starterNext.upload.title")}</p>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("starterNext.upload.hint")}</p>
          <button
            type="button"
            data-ux="starter_next_step:upload_open"
            onClick={() => {
              setUploadOpen((open) => {
                const next = !open;
                if (next) trackUxEvent("click", "starter_next_step:upload_open");
                return next;
              });
            }}
            className="mt-4 inline-flex items-center justify-center gap-1.5 rounded-lg border border-gray-300 dark:border-gray-700 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-200 transition-colors hover:bg-gray-50 dark:hover:bg-gray-800"
          >
            {uploadOpen ? t("starterNext.upload.toggle.close") : t("starterNext.upload.toggle.open")}
          </button>
        </div>
      </div>

      {uploadOpen && (
        <div className="mt-4">
          <UploadDeployCard projectId={projectId} envId={envId} compact />
        </div>
      )}
    </div>
  );
}

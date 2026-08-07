"use client";
import Link from "next/link";
import { GitBranch } from "lucide-react";
import { TemplateDeployCards } from "@/components/console/template-deploy-cards";
import { UploadDeployCard } from "@/components/deploy/upload-deploy";
import { useT } from "@/lib/i18n/console/context";

export interface EmptyProjectOnrampProps {
  projectId: string;
  envId: string | null;
}

/**
 * First screen of an empty project, ordered by what actually converts.
 *
 * Prod data (2026-08-04, all time): 10 apps arrived from GitHub, 2 from an
 * uploaded archive, 2 from a starter template — and zero template deploys since
 * the template hero shipped on 2026-07-22. Giving templates a hero card the same
 * size as Git therefore sold the least-used path hardest and buried the one
 * people came for. Here Git is the single primary action, the archive upload is
 * the explicit no-Git fallback, and the templates survive only as a one-line
 * showroom whose apps the demo reaper deletes after 24h.
 */
export function EmptyProjectOnramp({ projectId, envId }: EmptyProjectOnrampProps) {
  const { t } = useT();

  return (
    <div data-onboarding="first-deploy" className="mb-8">
      <div className="rounded-2xl border-2 border-blue-200 dark:border-blue-900/60 bg-gradient-to-br from-blue-50 to-white dark:from-blue-950/20 dark:to-gray-900 p-6 shadow-sm sm:p-8">
        <div className="flex flex-wrap items-start justify-between gap-6">
          <div className="min-w-0 max-w-xl">
            <h2 className="text-lg font-bold text-gray-900 dark:text-gray-100 sm:text-xl">
              {t("overview.onramp.git.title")}
            </h2>
            <p className="mt-2 text-sm text-gray-600 dark:text-gray-300 sm:text-base">
              {t("overview.onramp.git.hint")}
            </p>
          </div>
          <Link
            href={`/projects/${projectId}/git/import${envId ? `?envId=${envId}` : ""}`}
            className="inline-flex shrink-0 items-center gap-2 rounded-lg bg-blue-600 px-5 py-3 text-sm font-medium text-white transition-colors hover:bg-blue-700"
          >
            <GitBranch className="h-4 w-4" />
            {t("overview.onramp.git.cta")}
          </Link>
        </div>
      </div>

      <UploadDeployCard projectId={projectId} envId={envId} className="mt-4" />

      <div className="mt-4 rounded-xl border border-dashed border-gray-300 dark:border-gray-700 p-5">
        <div className="mb-3">
          <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">
            {t("overview.onramp.demo.title")}
          </h3>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {t("overview.onramp.demo.hint")}
          </p>
        </div>
        <TemplateDeployCards projectId={projectId} envId={envId} placement="onramp" compact />
      </div>
    </div>
  );
}

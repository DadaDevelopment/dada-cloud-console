"use client";

import { AlertTriangle } from "lucide-react";
import Link from "next/link";
import { useT } from "@/lib/i18n/console/context";
import { evaluateDeployDrift } from "@/lib/deploy-drift";
import type { Deployment } from "@/lib/types";

interface AppDeployDriftBannerProps {
  deployments: Deployment[] | null | undefined;
  deploymentsHref: string;
}

/**
 * Names the fact that the pod is running an older deployment than the
 * newest one recorded, without guessing why (same discipline as
 * `AppLastMileBanner`: state the observed fact, not a cause -- the platform
 * does not know one). Links straight to the deployments list, where the
 * rollback control already lives.
 *
 * Renders nothing when `evaluateDeployDrift` finds no drift or cannot tell
 * (see its doc comment) -- absence of data is never shown as a verdict.
 */
export function AppDeployDriftBanner({ deployments, deploymentsHref }: AppDeployDriftBannerProps) {
  const { t } = useT();
  const drift = evaluateDeployDrift(deployments);
  if (!drift) return null;

  return (
    <div className="mb-6 rounded-xl border border-amber-300 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/30 p-5 shadow-sm">
      <p className="flex items-center gap-1.5 text-sm font-semibold text-amber-800 dark:text-amber-300">
        <AlertTriangle className="h-4 w-4 shrink-0" />
        {t("apps.deployDrift.title")}
      </p>
      <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">{t("apps.deployDrift.desc")}</p>
      <Link
        href={deploymentsHref}
        className="mt-2 inline-block text-xs font-semibold text-amber-800 dark:text-amber-300 underline underline-offset-2"
      >
        {t("apps.deployDrift.viewDeployments")}
      </Link>
    </div>
  );
}

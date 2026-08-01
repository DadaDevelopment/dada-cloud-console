"use client";

import Link from "next/link";
import type { ReactNode } from "react";
import { Globe, GitBranch, Rocket } from "lucide-react";
import { useT } from "@/lib/i18n/console/context";
import type { NextStepId } from "@/lib/app-next-step";

interface StepDef {
  id: NextStepId;
  icon: ReactNode;
  titleKey: string;
  descKey: string;
}

const STEP_DEFS: Record<NextStepId, Omit<StepDef, "id">> = {
  connect_domain: {
    icon: <Globe className="h-5 w-5" />,
    titleKey: "apps.nextStep.domain.title",
    descKey: "apps.nextStep.domain.desc",
  },
  connect_git: {
    icon: <GitBranch className="h-5 w-5" />,
    titleKey: "apps.nextStep.connectGit.title",
    descKey: "apps.nextStep.connectGit.desc",
  },
  deploy_commit: {
    icon: <Rocket className="h-5 w-5" />,
    titleKey: "apps.nextStep.deploy.title",
    descKey: "apps.nextStep.deploy.desc",
  },
};

interface AppNextStepCardProps {
  steps: NextStepId[];
  onConnectDomain: () => void;
  gitSettingsHref: string;
  deploymentsHref: string;
}

/**
 * Shown when an app is `Ready` with no active alerts - a calm, informational
 * card (blue, not red/amber) pointing at what a healthy app still has left
 * to configure. Built entirely from data the app detail page already
 * fetched; see `lib/app-next-step.ts` for the selection logic.
 *
 * Each row carries `data-ux` so clicks are distinguishable in `ux_events`
 * via the existing document-click instrumentation (lib/ux-telemetry.ts) -
 * no new analytics transport.
 */
export function AppNextStepCard({ steps, onConnectDomain, gitSettingsHref, deploymentsHref }: AppNextStepCardProps) {
  const { t } = useT();

  if (steps.length === 0) return null;

  function hrefFor(id: NextStepId): string | undefined {
    if (id === "connect_git") return gitSettingsHref;
    if (id === "deploy_commit") return deploymentsHref;
    return undefined;
  }

  return (
    <div className="mb-6 rounded-xl border border-blue-100 dark:border-blue-950 bg-blue-50/50 dark:bg-blue-950/20 px-5 py-4">
      <p className="mb-3 text-xs font-semibold uppercase tracking-wide text-blue-600 dark:text-blue-400">
        {t("apps.nextStep.title")}
      </p>
      <div className="space-y-2">
        {steps.map((id) => {
          const def = STEP_DEFS[id];
          const href = hrefFor(id);
          const content = (
            <>
              <span className="shrink-0 text-blue-500 dark:text-blue-400">{def.icon}</span>
              <span className="min-w-0">
                <span className="block text-sm font-medium text-gray-900 dark:text-gray-100">{t(def.titleKey)}</span>
                <span className="block text-xs text-gray-500 dark:text-gray-400">{t(def.descKey)}</span>
              </span>
            </>
          );
          const rowClass =
            "flex items-center gap-3 rounded-lg border border-transparent bg-white dark:bg-gray-900 px-4 py-3 shadow-sm transition-colors hover:border-blue-200 dark:hover:border-blue-800";
          if (href) {
            return (
              <Link key={id} href={href} data-ux={`app_next_step:${id}`} className={rowClass}>
                {content}
              </Link>
            );
          }
          return (
            <button key={id} type="button" onClick={onConnectDomain} data-ux={`app_next_step:${id}`} className={`${rowClass} w-full text-left`}>
              {content}
            </button>
          );
        })}
      </div>
    </div>
  );
}

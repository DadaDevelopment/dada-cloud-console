"use client";

import Link from "next/link";
import { Rocket } from "lucide-react";
import { useT } from "@/lib/i18n/console/context";
import { Spinner } from "@/components/ui/spinner";
import { selectCurrentDeployment, shortImageRef } from "@/lib/current-deployment";
import type { Deployment } from "@/lib/types";

interface AppCurrentDeployCardProps {
  deployments: Deployment[] | null | undefined;
  deploymentsHref: string;
}

/**
 * Answers "did my deploy actually land, and when" directly on the app page --
 * the question a normal (non-technical-role) user has no way to answer
 * otherwise, since the raw image field further down is gated behind
 * `canSeeTechnical`. Grounded in the megafactory read: a user built, the
 * platform auto-deployed the image, but nothing on the page said so, so the
 * user re-deployed the same digest by hand a minute later.
 *
 * Renders nothing when `selectCurrentDeployment` has nothing honest to say
 * (no deployments yet, or the gap has gone on long enough that "pending"
 * would be a guess) -- absence of data is never shown as a verdict.
 */
export function AppCurrentDeployCard({ deployments, deploymentsHref }: AppCurrentDeployCardProps) {
  const { t } = useT();
  const state = selectCurrentDeployment(deployments);
  if (!state) return null;

  const image = shortImageRef(state.deployment.image_uri);

  if (state.kind === "pending") {
    return (
      <div className="mb-6 flex items-center gap-3 rounded-xl border border-blue-100 dark:border-blue-950 bg-blue-50/50 dark:bg-blue-950/20 px-5 py-4">
        <Spinner size="sm" />
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium text-gray-900 dark:text-gray-100" title={state.deployment.image_uri}>
            {t("apps.currentDeploy.pending", { image })}
          </p>
          <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
            {t("apps.currentDeploy.pendingAgo", { ago: localizedAgo(state.deployment.created_at, t) })}
          </p>
        </div>
        <Link
          href={deploymentsHref}
          className="shrink-0 text-xs font-medium text-blue-600 dark:text-blue-400 hover:underline"
        >
          {t("apps.deployDrift.viewDeployments")}
        </Link>
      </div>
    );
  }

  return (
    <div className="mb-6 flex items-center gap-3 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-4 shadow-sm">
      <Rocket className="h-4 w-4 shrink-0 text-gray-400 dark:text-gray-500" />
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium text-gray-900 dark:text-gray-100" title={state.deployment.image_uri}>
          {t("apps.currentDeploy.deployed", { image })}
        </p>
        <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
          {t("apps.currentDeploy.deployedAgo", { ago: localizedAgo(state.deployment.created_at, t) })}
        </p>
      </div>
      <Link
        href={deploymentsHref}
        className="shrink-0 text-xs font-medium text-blue-600 dark:text-blue-400 hover:underline"
      >
        {t("apps.deployDrift.viewDeployments")}
      </Link>
    </div>
  );
}

/**
 * Relative age in the console's own language. `lib/format`'s `timeAgo`
 * hardcodes English units, which reads as broken next to Russian copy, so
 * this surface counts the units itself and lets the message catalog spell
 * them -- same duplication as `build-provenance.tsx`'s `localizedAgo` and
 * `app-last-mile-banner.tsx`'s, kept local rather than shared because each
 * caller's rounding needs stayed simple enough not to be worth a shared
 * module.
 */
function localizedAgo(dateStr: string, t: ReturnType<typeof useT>["t"]): string {
  const secs = Math.max(0, Math.floor((Date.now() - new Date(dateStr).getTime()) / 1000));
  if (secs < 60) return t("common.time.agoSeconds", { n: secs });
  const mins = Math.floor(secs / 60);
  if (mins < 60) return t("common.time.agoMinutes", { n: mins });
  const hours = Math.floor(mins / 60);
  if (hours < 24) return t("common.time.agoHours", { n: hours });
  return t("common.time.agoDays", { n: Math.floor(hours / 24) });
}

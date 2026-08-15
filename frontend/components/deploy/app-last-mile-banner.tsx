"use client";

import { AlertTriangle } from "lucide-react";
import { useT } from "@/lib/i18n/console/context";
import { evaluateLastMile, type LastMileSummary } from "@/lib/last-mile-status";

interface AppLastMileBannerProps {
  summary: LastMileSummary | null | undefined;
}

/**
 * Relative age in the console's own language, matching the pattern already
 * used by `build-provenance.tsx`'s `localizedAgo`: `lib/format`'s `timeAgo`
 * hardcodes English units, which reads as broken next to a Russian sentence.
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

/**
 * The honest last-mile verdict: what a real visitor gets when they open the
 * app's public address right now, from an in-cluster probe -- not inferred
 * from build status or pod readiness. See `lib/last-mile-status.ts` for the
 * dead/alive boundary and gitops-agent's livenessprobe.go for the probe
 * itself.
 *
 * Renders nothing when the address is serving (2xx/3xx) and nothing when no
 * probe has run yet -- absence of data is never shown as health, and a
 * healthy address does not need a second green banner on top of
 * `AppLiveBanner`. Only renders when the probe caught the address dead, and
 * deliberately does not look like a healthy state: this sits next to
 * `AppAlertsBanner`'s red styling, not the amber "still attaching" one used
 * for `apps.url.failed`, because this is a confirmed fact, not a pending
 * step. States the observed status code and check time only -- never a
 * cause, since the platform does not know one (see the crash-banner
 * incident where a generic failure was misread as the user's own code).
 */
export function AppLastMileBanner({ summary }: AppLastMileBannerProps) {
  const { t } = useT();
  const verdict = evaluateLastMile(summary);
  if (!verdict) return null;

  const ago = localizedAgo(verdict.checkedAt, t);
  const label = verdict.status > 0
    ? t("apps.lastMile.statusLabel", { code: verdict.status, ago })
    : t("apps.lastMile.unreachableLabel", { ago });

  return (
    <div className="mb-6 rounded-xl border border-red-300 dark:border-red-900 bg-red-50 dark:bg-red-950/30 p-5 shadow-sm">
      <p className="flex items-center gap-1.5 text-sm font-semibold text-red-800 dark:text-red-300">
        <AlertTriangle className="h-4 w-4 shrink-0" />
        {label}
      </p>
      <p className="mt-1 text-xs text-red-700 dark:text-red-400">{t("apps.lastMile.desc")}</p>
    </div>
  );
}

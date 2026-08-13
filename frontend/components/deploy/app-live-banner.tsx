"use client";

import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { Check, Copy, ExternalLink, X } from "lucide-react";
import { useT } from "@/lib/i18n/console/context";
import { trackUxEvent } from "@/lib/ux-telemetry";
import { dismissLiveBanner, isAppLive, isLiveBannerDismissed, subscribeLiveBannerDismissal } from "@/lib/app-live-banner";

interface AppLiveBannerProps {
  projectId: string;
  appName: string;
  url: string | null | undefined;
  phase: string | null | undefined;
  urlStatus: string | null | undefined;
  urlReason: string | null | undefined;
}

/**
 * Durable "your app is live" confirmation on the app detail page.
 *
 * Unlike the build-finish panel in `BuildWatcher` (only visible in the tab
 * that started the build, see lib/build-watch.ts for the measured gap this
 * closes), this banner is derived from the persisted resource snapshot on
 * every page load, so it survives navigation away and a reload, and keeps
 * showing until the user explicitly dismisses it. See lib/app-live-banner.ts
 * for the exact show condition and the localStorage dismissal key.
 *
 * Every control carries `data-ux` so the shown -> opened / copied / dismissed
 * funnel is measurable the same way `app-next-step-card.tsx` already does
 * for its own row clicks: an explicit `view` event on first render, clicks
 * picked up automatically by the document-click instrumentation.
 */
export function AppLiveBanner({ projectId, appName, url, phase, urlStatus, urlReason }: AppLiveBannerProps) {
  const { t } = useT();
  const [copied, setCopied] = useState(false);
  const viewedRef = useRef(false);

  const readDismissed = useCallback(() => isLiveBannerDismissed(projectId, appName), [projectId, appName]);
  const dismissed = useSyncExternalStore(subscribeLiveBannerDismissal, readDismissed, () => true);
  const visible = !dismissed && isAppLive({ url, phase, urlStatus, urlReason });

  useEffect(() => {
    if (visible && !viewedRef.current) {
      viewedRef.current = true;
      trackUxEvent("view", "app_live_banner:shown");
    }
  }, [visible]);

  if (!visible || !url) return null;

  function handleDismiss() {
    dismissLiveBanner(projectId, appName);
  }

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(url as string);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      return;
    }
  }

  return (
    <div className="mb-6 rounded-xl border border-green-300 dark:border-green-800 bg-green-50 dark:bg-green-950/20 p-5 shadow-sm">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-sm font-semibold text-green-800 dark:text-green-300">{t("apps.liveBanner.title")}</p>
          <p className="mt-0.5 text-xs text-green-700 dark:text-green-400">{t("apps.liveBanner.desc")}</p>
          <p className="mt-2 break-all font-mono text-base font-semibold text-gray-900 dark:text-gray-100">{url}</p>
        </div>
        <button
          type="button"
          onClick={handleDismiss}
          data-ux="app_live_banner:dismiss"
          aria-label={t("apps.liveBanner.dismiss")}
          className="shrink-0 rounded-md p-1 text-green-700 dark:text-green-400 hover:bg-green-100 dark:hover:bg-green-900/40 transition-colors"
        >
          <X className="h-4 w-4" />
        </button>
      </div>
      <div className="mt-3 flex flex-wrap gap-2">
        <a
          href={url}
          target="_blank"
          rel="noopener noreferrer"
          data-ux="app_live_banner:open"
          className="inline-flex items-center gap-1.5 rounded-lg bg-green-600 px-3 py-1.5 text-xs font-semibold text-white shadow-sm hover:bg-green-700 transition-colors"
        >
          <ExternalLink className="h-3.5 w-3.5" />
          {t("apps.liveBanner.open")}
        </a>
        <button
          type="button"
          onClick={handleCopy}
          data-ux="app_live_banner:copy"
          className="inline-flex items-center gap-1.5 rounded-lg border border-green-300 dark:border-green-800 bg-white dark:bg-gray-900 px-3 py-1.5 text-xs font-medium text-green-700 dark:text-green-300 hover:bg-green-50 dark:hover:bg-green-950/30 transition-colors"
        >
          {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
          {copied ? t("apps.liveBanner.copied") : t("apps.liveBanner.copy")}
        </button>
      </div>
    </div>
  );
}

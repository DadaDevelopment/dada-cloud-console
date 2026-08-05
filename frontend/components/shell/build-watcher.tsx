"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { buildsApi, appsApi } from "@/lib/api";
import type { BuildStatus } from "@/lib/types";
import { isBuildActive } from "@/components/deploy/build-status-badge";
import { BUILD_TRACK_EVENT, readTrackedBuilds, untrackBuild, type TrackedBuild } from "@/lib/build-watch";
import { showBuildNotification } from "@/lib/build-notify";
import { useT } from "@/lib/i18n/console/context";
import { trackUxEvent } from "@/lib/ux-telemetry";

const POLL_MS = 3000;
const APP_READY_POLL_MS = 5000;
const APP_READY_MAX_POLLS = 60;
const TITLE_FLASH_MS = 1200;

type NoticeStatus = Extract<BuildStatus, "success" | "failed">;

/** Route to a tracked build's detail page, shared by the panel and the native notification. */
function buildHref(notice: Pick<Notice, "projectId" | "appName" | "buildId" | "envId">): string {
  return `/projects/${notice.projectId}/apps/${notice.appName}/builds/${notice.buildId}${
    notice.envId ? `?envId=${notice.envId}` : ""
  }`;
}

interface Notice {
  buildId: string;
  projectId: string;
  envId: string;
  appName: string;
  status: NoticeStatus;
  appUrl: string | null;
  appReady: boolean;
  appPolls: number;
}

/**
 * Global build-finish notifier.
 *
 * The build detail page only shows the "your app is live" panel while the
 * user is still sitting on that page when the build resolves. Most people
 * who trigger a manual build leave first, so that moment is never shown to
 * them and they conclude nothing happened (see `lib/build-watch.ts`).
 *
 * This component tracks builds recorded by `trackBuildStart` (localStorage,
 * survives navigation and reload), polls their status while any are
 * in-progress, and surfaces a dismissible bottom-right notice the moment one
 * finishes -- wherever in the console the user currently is. Idle: no
 * tracked builds means no polling at all.
 */
export function BuildWatcher() {
  const { t } = useT();
  const [tracked, setTracked] = useState<TrackedBuild[]>(() => readTrackedBuilds());
  const [notices, setNotices] = useState<Notice[]>([]);
  const [unread, setUnread] = useState(false);
  const viewedRef = useRef<Set<string>>(new Set());
  const notifiedRef = useRef<Set<string>>(new Set());

  const dropTracked = useCallback((buildId: string) => {
    untrackBuild(buildId);
    setTracked((prev) => prev.filter((b) => b.buildId !== buildId));
  }, []);

  /**
   * Keeps the in-memory list in step with storage after the initial mount.
   *
   * This component lives in the console layout, which survives every
   * client-side navigation, so it mounts once per full page load. Each caller
   * of `trackBuildStart` runs inside that same layout without a reload, which
   * means the build the user just triggered was written to storage after this
   * mount read it. Listening to `BUILD_TRACK_EVENT` covers that (same-tab)
   * case; `storage` covers a build triggered in another tab, where the browser
   * refuses to fire the same-tab event.
   *
   * The re-read replaces state only when the set of tracked build ids actually
   * changed, so an announcement caused by this component's own `untrackBuild`
   * does not hand the polling effect a fresh array identity and restart it.
   */
  useEffect(() => {
    function sync() {
      const next = readTrackedBuilds();
      setTracked((prev) => {
        const same =
          prev.length === next.length &&
          prev.every((entry, i) => entry.buildId === next[i]?.buildId);
        return same ? prev : next;
      });
    }
    window.addEventListener(BUILD_TRACK_EVENT, sync);
    window.addEventListener("storage", sync);
    return () => {
      window.removeEventListener(BUILD_TRACK_EVENT, sync);
      window.removeEventListener("storage", sync);
    };
  }, []);

  useEffect(() => {
    if (tracked.length === 0) return;
    let canceled = false;
    const poll = () => {
      tracked.forEach((entry) => {
        buildsApi
          .get(entry.projectId, entry.buildId)
          .then(({ build }) => {
            if (canceled) return;
            if (isBuildActive(build.status)) return;
            dropTracked(entry.buildId);
            if (build.status !== "success" && build.status !== "failed") return;
            setNotices((prev) => {
              if (prev.some((n) => n.buildId === entry.buildId)) return prev;
              return [
                ...prev,
                {
                  buildId: entry.buildId,
                  projectId: entry.projectId,
                  envId: entry.envId,
                  appName: entry.appName,
                  status: build.status as NoticeStatus,
                  appUrl: null,
                  appReady: false,
                  appPolls: 0,
                },
              ];
            });
            setUnread(true);
          })
          .catch(() => undefined);
      });
    };
    poll();
    const timer = setInterval(poll, POLL_MS);
    return () => {
      canceled = true;
      clearInterval(timer);
    };
  }, [tracked, dropTracked]);

  useEffect(() => {
    const pending = notices.filter(
      (n) => n.status === "success" && !n.appReady && n.appPolls < APP_READY_MAX_POLLS,
    );
    if (pending.length === 0) return;
    let canceled = false;
    const read = () => {
      pending.forEach((notice) => {
        appsApi
          .list(notice.projectId, notice.envId)
          .then((data) => {
            if (canceled) return;
            const found = (data.apps ?? []).find((a) => a.name === notice.appName);
            const summary = found?.summary_json as { url?: string } | undefined;
            const ready = (found?.phase ?? "").toLowerCase() === "ready";
            setNotices((prev) =>
              prev.map((n) =>
                n.buildId === notice.buildId
                  ? { ...n, appUrl: summary?.url ?? n.appUrl, appReady: ready, appPolls: n.appPolls + 1 }
                  : n,
              ),
            );
          })
          .catch(() => undefined);
      });
    };
    const timer = setInterval(read, APP_READY_POLL_MS);
    return () => {
      canceled = true;
      clearInterval(timer);
    };
  }, [notices]);

  useEffect(() => {
    notices.forEach((n) => {
      const key = `${n.buildId}:${n.status}`;
      if (viewedRef.current.has(key)) return;
      viewedRef.current.add(key);
      trackUxEvent("view", `build_notice:${n.status === "success" ? "success" : "failure"}`);
    });
  }, [notices]);

  /**
   * Fires the native notification for a background tab. Guarded by
   * `notifiedRef` the same way `viewedRef` guards the view event above, so a
   * re-render never raises the same build twice. `showBuildNotification`
   * itself decides whether anything actually happened (permission, tab
   * visibility); telemetry only fires for a real "shown".
   */
  useEffect(() => {
    notices.forEach((n) => {
      const key = `${n.buildId}:${n.status}`;
      if (notifiedRef.current.has(key)) return;
      notifiedRef.current.add(key);
      const success = n.status === "success";
      const href = buildHref(n);
      const shown = showBuildNotification({
        title: t(success ? "buildWatcher.notify.success.title" : "buildWatcher.notify.failure.title"),
        body: t(success ? "buildWatcher.notify.success.body" : "buildWatcher.notify.failure.body", {
          app: n.appName,
        }),
        tag: n.buildId,
        onClick: () => {
          trackUxEvent("click", "build_notify:click");
          window.location.href = href;
        },
      });
      if (shown) trackUxEvent("view", `build_notify:shown:${success ? "success" : "failure"}`);
    });
  }, [notices, t]);

  useEffect(() => {
    function onFocus() {
      setUnread(false);
    }
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, []);

  /**
   * Flags the unread notice in the tab title for a user who is looking at
   * another tab. The base title is re-read from the document on every tick
   * rather than captured once: `DocumentTitle` owns the title and rewrites it
   * on each navigation, so a captured base would pin the tab to the name of
   * whatever page the notice happened to arrive on. Cleanup strips the flag
   * off the current title for the same reason.
   */
  useEffect(() => {
    if (!unread || notices.length === 0) return;
    const prefix = notices.some((n) => n.status === "failed") ? "⚠️ " : "✅ ";
    const strip = (title: string) => (title.startsWith(prefix) ? title.slice(prefix.length) : title);
    let on = false;
    const timer = setInterval(() => {
      on = !on;
      const base = strip(document.title);
      document.title = on ? `${prefix}${base}` : base;
    }, TITLE_FLASH_MS);
    return () => {
      clearInterval(timer);
      document.title = strip(document.title);
    };
  }, [unread, notices]);

  function dismiss(notice: Notice) {
    setNotices((prev) => {
      const next = prev.filter((n) => n.buildId !== notice.buildId);
      if (next.length === 0) setUnread(false);
      return next;
    });
  }

  if (notices.length === 0) return null;

  return (
    <div className="fixed inset-x-4 bottom-24 z-40 flex flex-col gap-2 sm:inset-x-auto sm:right-5 sm:w-96">
      {notices.map((notice) => {
        const success = notice.status === "success";
        const href = buildHref(notice);
        return (
          <div
            key={notice.buildId}
            className={`rounded-lg border px-4 py-3 text-sm shadow-lg ${
              success
                ? "border-green-200 bg-green-50 text-green-700 dark:border-green-900 dark:bg-green-950/90 dark:text-green-300"
                : "border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/90 dark:text-red-300"
            }`}
          >
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <p className="font-medium">
                  {t(success ? "buildWatcher.success.title" : "buildWatcher.failure.title")}
                </p>
                <p className="mt-0.5 truncate opacity-90">
                  {t(success ? "buildWatcher.success.body" : "buildWatcher.failure.body", {
                    app: notice.appName,
                  })}
                </p>
              </div>
              <button
                onClick={() => dismiss(notice)}
                data-ux="build_notice:dismiss"
                aria-label={t("buildWatcher.dismiss")}
                className="shrink-0 rounded-md p-1 opacity-70 hover:opacity-100"
              >
                &times;
              </button>
            </div>
            <div className="mt-2 flex flex-wrap items-center gap-3">
              {success && notice.appUrl && notice.appReady && (
                <a
                  href={notice.appUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                  data-ux="build_notice:open_app"
                  className="rounded-md bg-green-600 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-green-700"
                >
                  {t("buildWatcher.success.openApp")}
                </a>
              )}
              <Link
                href={href}
                data-ux="build_notice:open_build"
                className="text-xs font-medium underline underline-offset-2 opacity-90 hover:opacity-100"
              >
                {t("buildWatcher.openBuild")}
              </Link>
            </div>
          </div>
        );
      })}
    </div>
  );
}

import { trackUxEvent } from "@/lib/ux-telemetry";

/**
 * Native browser notifications for build completion.
 *
 * `BuildWatcher` (components/shell/build-watcher.tsx) already shows a
 * bottom-right panel when a tracked build resolves, but that panel is only
 * visible while the console tab is on screen. A user who triggers a build and
 * switches to another tab or app gets no signal at all -- see
 * `lib/build-watch.ts` for the measured leak this closes.
 *
 * PERMISSION. `Notification.requestPermission()` must run inside a user
 * gesture in current browsers, so `maybeRequestNotifyPermission` is called
 * from `trackBuildStart`, which fires right after the user clicks a "build"
 * button. Asked at most once ever: a prior dismissal (or grant) is
 * remembered in localStorage so the console never re-prompts.
 *
 * SAFETY. Every entry point guards for SSR (`typeof window`), for browsers
 * without `Notification` (Safari on iOS, insecure contexts), for a denied or
 * unresolved permission, and for a constructor throw -- all of it collapses
 * to a silent no-op. This module must never be able to break the product it
 * is trying to make more visible.
 */

const ASKED_STORAGE_KEY = "dada_build_notify_asked";

function hasNotificationApi(): boolean {
  return typeof window !== "undefined" && typeof window.Notification !== "undefined";
}

function rememberAsked(): void {
  try {
    window.localStorage.setItem(ASKED_STORAGE_KEY, "1");
  } catch {
    return;
  }
}

/** Raising the window is a courtesy, not a requirement: a blocked focus must not swallow the click. */
function focusWindow(): void {
  try {
    window.focus();
  } catch {
    return;
  }
}

function alreadyAsked(): boolean {
  try {
    return window.localStorage.getItem(ASKED_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

/**
 * Requests notification permission, but only inside the calling user gesture
 * and only once ever. No-ops entirely when the permission is already
 * decided (`granted` or `denied`) or was already asked in a prior visit.
 *
 * The "already asked" flag is written only once the browser reports a real
 * decision. A prompt the browser refused to raise -- the caller runs after an
 * `await`, so transient user activation may already have expired -- leaves
 * the permission at `default`, and burning the flag there would silently
 * disable this feature forever on that browser.
 */
export function maybeRequestNotifyPermission(): void {
  if (!hasNotificationApi()) return;
  try {
    if (window.Notification.permission !== "default") return;
    if (alreadyAsked()) return;
    window.Notification.requestPermission()
      .then((permission) => {
        if (permission !== "granted" && permission !== "denied") return;
        rememberAsked();
        trackUxEvent(
          "goal",
          permission === "granted" ? "build_notify:permission_granted" : "build_notify:permission_denied",
        );
      })
      .catch(() => undefined);
  } catch {
    return;
  }
}

export interface ShowBuildNotificationArgs {
  title: string;
  body: string;
  tag: string;
  onClick: () => void;
}

/**
 * Best-effort native notification. Silently does nothing unless permission
 * was already granted and the tab is currently in the background -- the
 * in-page panel already covers the foreground case, and a duplicate would
 * just be noise. Returns whether a notification was actually raised, so the
 * caller can tell "not shown" apart from "shown" for telemetry.
 */
export function showBuildNotification({ title, body, tag, onClick }: ShowBuildNotificationArgs): boolean {
  if (!hasNotificationApi()) return false;
  if (typeof document === "undefined") return false;
  try {
    if (window.Notification.permission !== "granted") return false;
    if (document.visibilityState === "visible") return false;
    const notification = new window.Notification(title, { body, tag });
    notification.onclick = () => {
      focusWindow();
      onClick();
      notification.close();
    };
    return true;
  } catch {
    return false;
  }
}

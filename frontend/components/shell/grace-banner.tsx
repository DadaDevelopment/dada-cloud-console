"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { billingApi } from "@/lib/api";
import type { AccountSummary } from "@/lib/api";
import { quotaPressure, quotaUpgradeHref } from "@/lib/billing-quota";
import { useProjectContext } from "@/lib/project-context";
import { trackUxEvent } from "@/lib/ux-telemetry";
import { useT } from "@/lib/i18n/console/context";

const DISMISS_KEY = "dada_grace_banner_dismissed_until";

/** Resources counted against the plan, in the order a user runs into them. */
const QUOTA_ORDER = ["apps", "databases", "domains", "team_members"] as const;

/**
 * Reads the remembered dismissal without touching state during an effect.
 *
 * Safe as a lazy initialiser: the banner renders nothing until the account
 * summary arrives over the network, so server and first client render agree
 * on `null` output regardless of what storage holds.
 */
function readDismissed(): string | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage.getItem(DISMISS_KEY);
  } catch {
    return null;
  }
}

/**
 * Announces the end of the grandfathering window to the accounts it actually
 * affects.
 *
 * These users were promised a free tier and some are already above it. The
 * window is silent by construction — the quota gate simply stops refusing —
 * so without this the first news of a limit is a 403 that reads like a bug.
 * The email sweeper carries the same message; this is the copy that reaches
 * people who never open their mail.
 *
 * It renders for accounts inside a grace window that are either already over a
 * limit or sitting exactly on one. The second case is the larger group and used
 * to be silent: an account at 1 of 1 apps loses nothing on the deadline, but
 * the next app it creates is the one refused, and it learned that only from the
 * 403. An org comfortably under every limit is still never told about a wall it
 * will not hit. Dismissal is remembered per deadline: dismissing the 30-day
 * notice does not silence the one sent the day before.
 *
 * Every failure path here is swallowed: a summary fetch that fails renders
 * nothing, and a localStorage that throws (private mode) only costs the
 * dismissal memory. Billing chrome must never break the shell.
 */
export function GraceBanner() {
  const { t } = useT();
  const { projectId, defaultProjectId } = useProjectContext();
  const [summary, setSummary] = useState<AccountSummary | null>(null);
  const [dismissedFor, setDismissedFor] = useState<string | null>(readDismissed);

  useEffect(() => {
    let cancelled = false;
    billingApi
      .accountSummary()
      .then((data) => {
        if (!cancelled) setSummary(data);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  const graceUntil = summary?.quota_grace_until ?? null;
  const { over, at } = quotaPressure(summary?.quotas ?? null, QUOTA_ORDER);
  const pressured = over.length > 0 || at.length > 0;
  const upgradeHref = quotaUpgradeHref(projectId ?? defaultProjectId);
  const visible = Boolean(graceUntil) && dismissedFor !== graceUntil && pressured;

  useEffect(() => {
    if (!visible) return;
    trackUxEvent("view", over.length > 0 ? "grace_banner:over_limit" : "grace_banner:at_limit");
  }, [visible, over.length]);

  if (!graceUntil || dismissedFor === graceUntil) return null;
  if (!pressured) return null;

  const deadline = new Date(graceUntil);
  const dateLabel = Number.isNaN(deadline.getTime()) ? graceUntil : deadline.toLocaleDateString();
  const label = (keys: string[]) => keys.map((key) => t(`spend.quota.${key}`).toLowerCase()).join(", ");

  function dismiss() {
    if (!graceUntil) return;
    trackUxEvent("click", "grace_banner:dismiss");
    try {
      window.localStorage.setItem(DISMISS_KEY, graceUntil);
    } catch {
      setDismissedFor(graceUntil);
    }
    setDismissedFor(graceUntil);
  }

  return (
    <div className="flex items-start gap-3 border-b border-amber-300 bg-amber-50 px-4 py-2.5 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-100">
      <p className="flex-1">
        {over.length > 0
          ? t("grace.banner.text", { date: dateLabel, resources: label(over) })
          : t("grace.banner.atLimit", { date: dateLabel, resources: label(at) })}{" "}
        {upgradeHref && (
          <Link
            href={upgradeHref}
            data-ux="grace_banner:upgrade"
            onClick={() => trackUxEvent("click", "grace_banner:upgrade")}
            className="font-medium underline underline-offset-2"
          >
            {t("grace.banner.cta")}
          </Link>
        )}
      </p>
      <button
        type="button"
        onClick={dismiss}
        aria-label={t("grace.banner.dismiss")}
        data-ux="grace_banner:dismiss"
        className="shrink-0 rounded px-1.5 text-amber-700 transition-colors hover:bg-amber-100 dark:text-amber-300 dark:hover:bg-amber-900"
      >
        ✕
      </button>
    </div>
  );
}

"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { billingApi } from "@/lib/api";
import type { AccountSummary } from "@/lib/api";
import { useT } from "@/lib/i18n/console/context";

const DISMISS_KEY = "dada_grace_banner_dismissed_until";

/** Resources counted against the plan, in the order a user runs into them. */
const QUOTA_ORDER = ["apps", "databases", "domains", "team_members"] as const;

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
 * It renders only when the account is BOTH inside a grace window and over a
 * limit, so an org that fits in the free tier is never told about a wall it
 * will not hit. Dismissal is remembered per deadline: dismissing the 30-day
 * notice does not silence the one sent the day before.
 *
 * Every failure path here is swallowed: a summary fetch that fails renders
 * nothing, and a localStorage that throws (private mode) only costs the
 * dismissal memory. Billing chrome must never break the shell.
 */
export function GraceBanner() {
  const { t } = useT();
  const [summary, setSummary] = useState<AccountSummary | null>(null);
  const [dismissedFor, setDismissedFor] = useState<string | null>(null);

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

  useEffect(() => {
    try {
      setDismissedFor(window.localStorage.getItem(DISMISS_KEY));
    } catch {
      setDismissedFor(null);
    }
  }, []);

  const graceUntil = summary?.quota_grace_until ?? null;
  if (!graceUntil || dismissedFor === graceUntil) return null;

  const over = QUOTA_ORDER.filter((key) => {
    const row = summary?.quotas?.[key];
    return row && row.limit > 0 && row.used > row.limit;
  });
  if (over.length === 0) return null;

  const deadline = new Date(graceUntil);
  const dateLabel = Number.isNaN(deadline.getTime()) ? graceUntil : deadline.toLocaleDateString();
  const overLabel = over.map((key) => t(`spend.quota.${key}`).toLowerCase()).join(", ");

  function dismiss() {
    if (!graceUntil) return;
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
        {t("grace.banner.text", { date: dateLabel, resources: overLabel })}{" "}
        <Link href="/pricing" className="font-medium underline underline-offset-2">
          {t("grace.banner.cta")}
        </Link>
      </p>
      <button
        type="button"
        onClick={dismiss}
        aria-label={t("grace.banner.dismiss")}
        className="shrink-0 rounded px-1.5 text-amber-700 transition-colors hover:bg-amber-100 dark:text-amber-300 dark:hover:bg-amber-900"
      >
        ✕
      </button>
    </div>
  );
}

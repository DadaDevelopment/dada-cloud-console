"use client";
import { useEffect, useState } from "react";
import { appsApi } from "@/lib/api";
import { useT } from "@/lib/i18n/console/context";

export interface DemoAppChipProps {
  projectId: string;
  envId: string;
  appName: string;
  expiresAt: string;
}

/**
 * Countdown badge for an app deployed from a platform starter template, plus the
 * one-click escape from it.
 *
 * A showroom deploy exists to prove the platform works, not to be kept, and the
 * ones nobody claimed used to sit in customer projects for weeks (one starter
 * app was Ready for 18 days in a project that never deployed its own code). The
 * backend now deletes them on a deadline, so this chip is the contract with the
 * user: the deletion is visible before it happens, and one press cancels it
 * permanently.
 */
export function DemoAppChip({ projectId, envId, appName, expiresAt }: DemoAppChipProps) {
  const { t } = useT();
  const [kept, setKept] = useState(false);
  const [busy, setBusy] = useState(false);
  const [hoursLeft, setHoursLeft] = useState<number | null>(null);

  useEffect(() => {
    function recompute() {
      const msLeft = new Date(expiresAt).getTime() - Date.now();
      setHoursLeft(Number.isNaN(msLeft) ? null : Math.max(0, Math.ceil(msLeft / 3_600_000)));
    }
    recompute();
    const timer = setInterval(recompute, 60_000);
    return () => clearInterval(timer);
  }, [expiresAt]);

  async function keep(e: React.MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
    if (busy) return;
    setBusy(true);
    try {
      await appsApi.keepDemo(projectId, envId, appName);
      setKept(true);
    } finally {
      setBusy(false);
    }
  }

  if (kept || hoursLeft === null) return null;

  return (
    <span className="inline-flex items-center gap-1.5 rounded-full bg-amber-100 dark:bg-amber-950/60 px-2 py-0.5 text-xs font-medium text-amber-800 dark:text-amber-300">
      {t("apps.demo.chip", { hours: String(hoursLeft) })}
      <button
        type="button"
        onClick={keep}
        disabled={busy}
        className="underline underline-offset-2 hover:no-underline disabled:opacity-60"
      >
        {t("apps.demo.keep")}
      </button>
    </span>
  );
}

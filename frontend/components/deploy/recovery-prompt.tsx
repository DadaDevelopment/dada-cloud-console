"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { X } from "lucide-react";
import { useT } from "@/lib/i18n/console/context";
import { recoveryApi } from "@/lib/api";
import {
  dismissRecoveryPrompt,
  recoveryPromptHref,
  shouldShowRecoveryPrompt,
} from "@/lib/recovery-prompt";
import { timeAgo } from "@/lib/format";
import { trackUxEvent } from "@/lib/ux-telemetry";
import type { RecoveryPrompt as RecoveryPromptData } from "@/lib/types";

interface RecoveryPromptProps {
  /**
   * Where in the product this instance is mounted, e.g. "apps-empty". Feeds
   * the `data-ux` names so a card mounted on two screens never collapses
   * into one bucket.
   */
  placement: string;
}

const COPY_KEYS: Record<RecoveryPromptData["kind"], { title: string; body: string; cta: string }> = {
  solution_install_env_failed: {
    title: "recovery.install.title",
    body: "recovery.install.body",
    cta: "recovery.install.cta",
  },
  payment_recurring_forbidden: {
    title: "recovery.payment.title",
    body: "recovery.payment.body",
    cta: "recovery.payment.cta",
  },
};

/**
 * "We broke it, we fixed it, here is the button" -- the one product-side
 * lever for platform bugs that silently broke a user's own action, since
 * emailing users about it is forbidden (owner, 2026-07-30). Fetches
 * GET /api/v1/recovery-prompt (backend/internal/api/platform_recovery.go)
 * and renders at most one card, matching `AppLiveBanner`'s and
 * `AppNextStepCard`'s visual and telemetry conventions.
 *
 * The backend already narrows eligibility (failure predates the fix, no
 * self-recovery, and for the install kind zero apps) -- this component only
 * renders what it is told and never re-derives those rules.
 *
 * Dismissal is CLIENT-SIDE ONLY: a localStorage flag keyed by kind +
 * failed_at (lib/recovery-prompt.ts). There is no server-side record of a
 * dismissal, so clearing site data or opening a different browser shows the
 * offer again -- an accepted tradeoff, the same one already made for
 * `AppLiveBanner`'s dismissal.
 */
export function RecoveryPrompt({ placement }: RecoveryPromptProps) {
  const { t } = useT();
  const [prompt, setPrompt] = useState<RecoveryPromptData | null>(null);
  const shownRef = useRef(false);

  useEffect(() => {
    let cancelled = false;
    recoveryApi
      .get()
      .then((res) => {
        if (!cancelled) setPrompt(res.prompt);
      })
      .catch(() => {
        return;
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const visible = prompt !== null && shouldShowRecoveryPrompt(prompt);

  useEffect(() => {
    if (visible && prompt && !shownRef.current) {
      shownRef.current = true;
      trackUxEvent("view", `recovery.${placement}.shown.${prompt.kind}`);
    }
  }, [visible, prompt, placement]);

  if (!visible || !prompt) return null;

  const copy = COPY_KEYS[prompt.kind];
  const href = recoveryPromptHref(prompt);

  function handleDismiss() {
    if (!prompt) return;
    dismissRecoveryPrompt(prompt.kind, prompt.failed_at);
    setPrompt(null);
  }

  return (
    <div className="mb-6 rounded-xl border border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/20 p-5 shadow-sm">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-sm font-semibold text-blue-900 dark:text-blue-200">{t(copy.title)}</p>
          <p className="mt-1 text-xs text-blue-800 dark:text-blue-300">
            {t(copy.body, { resource: prompt.resource_name, time: timeAgo(prompt.fixed_at) })}
          </p>
        </div>
        <button
          type="button"
          onClick={handleDismiss}
          data-ux={`recovery.${placement}.dismiss.${prompt.kind}`}
          aria-label={t("recovery.dismiss")}
          className="shrink-0 rounded-md p-1 text-blue-700 dark:text-blue-400 hover:bg-blue-100 dark:hover:bg-blue-900/40 transition-colors"
        >
          <X className="h-4 w-4" />
        </button>
      </div>
      <div className="mt-3">
        <Link
          href={href}
          data-ux={`recovery.${placement}.retry.${prompt.kind}`}
          className="inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-3 py-1.5 text-xs font-semibold text-white shadow-sm hover:bg-blue-700 transition-colors"
        >
          {t(copy.cta)}
        </Link>
      </div>
    </div>
  );
}

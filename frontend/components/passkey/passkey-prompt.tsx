"use client";
import { useCallback, useEffect, useState } from "react";
import { api } from "@/lib/api";
import { PasskeyCard } from "./passkey-card";
import {
  consumePasskeyActionStatus,
  isPasskeyModeEnabled,
  isPasskeySupported,
  startPasskeyEnrollment,
} from "@/lib/passkey";

/**
 * Onboarding campaign key for the passkey nudge. Shares the `user_onboarding`
 * table with the Joyride campaigns, so "Maybe Later" is remembered per user
 * across devices and the monotonic upsert makes `done`/`skipped` terminal —
 * the prompt asks once, not every login.
 */
const PASSKEY_KEY = "passkey";

/** Lets the page settle before the modal takes over the screen. */
const DELAY_MS = 4000;

/**
 * One-time in-console offer to enroll a passkey.
 *
 * The realm has had the passwordless WebAuthn policy and the
 * `webauthn-register-passwordless` required action live for weeks with zero
 * credentials registered, because the only entry point was Keycloak's Account
 * Console. This surfaces the same enrollment as a single click on the screen
 * the user is already on.
 *
 * Two things settle the campaign without ever showing the modal:
 * returning from Keycloak with a `kc_action_status` (the user just answered the
 * offer — `success` records `done`, `cancelled`/`error` records `skipped` so a
 * dismissed browser prompt does not nag on the next page load), and another
 * campaign sitting at `seen`, which means its Joyride tour is mid-flight and a
 * modal would bury the spotlight.
 */
export function PasskeyPrompt({ onOpenChange }: { onOpenChange?: (open: boolean) => void }) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);

  const report = useCallback((status: "seen" | "skipped" | "done") => {
    void api.onboarding.report(PASSKEY_KEY, { status, step: 0 }).catch(() => {});
  }, []);

  useEffect(() => {
    onOpenChange?.(open);
  }, [open, onOpenChange]);

  useEffect(() => {
    if (!isPasskeyModeEnabled() || !isPasskeySupported()) return;

    let alive = true;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const actionStatus = consumePasskeyActionStatus();
    if (actionStatus) {
      report(actionStatus === "success" ? "done" : "skipped");
      return;
    }

    api.onboarding
      .status()
      .then((map) => {
        if (!alive) return;
        const own = map[PASSKEY_KEY];
        if (own === "done" || own === "skipped") return;
        if (Object.entries(map).some(([key, status]) => key !== PASSKEY_KEY && status === "seen")) {
          return;
        }
        timer = setTimeout(() => {
          if (!alive) return;
          setOpen(true);
          report("seen");
        }, DELAY_MS);
      })
      .catch(() => {});

    return () => {
      alive = false;
      if (timer) clearTimeout(timer);
    };
  }, [report]);

  if (!open) return null;

  function handleLater() {
    report("skipped");
    setOpen(false);
  }

  function handleCreate() {
    setBusy(true);
    void startPasskeyEnrollment(window.location.pathname + window.location.search).catch(() => {
      setBusy(false);
    });
  }

  return <PasskeyCard busy={busy} onLater={handleLater} onCreate={handleCreate} />;
}

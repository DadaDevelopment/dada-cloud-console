"use client";
import { useCallback, useEffect, useState } from "react";
import { api } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { PasskeyCard } from "./passkey-card";
import {
  consumeFreshAuthentication,
  consumePasskeyActionStatus,
  isPasskeyModeEnabled,
  isPasskeySupported,
  isRecentAuthentication,
  startPasskeyEnrollment,
} from "@/lib/passkey";

/**
 * Onboarding campaign key for the passkey nudge. Shares the `user_onboarding`
 * table with the Joyride campaigns, so "Maybe Later" is remembered per user
 * across devices and the monotonic upsert makes `done`/`skipped` terminal —
 * the prompt asks once, not every login.
 */
const PASSKEY_KEY = "passkey";

/** Lets the first console screen paint before the modal takes over. */
const SETTLE_MS = 800;

/**
 * One-time in-console offer to enroll a passkey, shown right after a sign-in.
 *
 * The realm has had the passwordless WebAuthn policy and the
 * `webauthn-register-passwordless` required action live for weeks with zero
 * credentials registered, because the only entry point was the identity
 * provider's own account console. This surfaces the same enrollment as a single
 * click on the screen the user is already on.
 *
 * Timing is deliberate and gated twice, because "came back from the identity
 * provider" is NOT the same as "signed in". Reopening the console also runs a
 * full authorize round-trip through `/callback`, it just gets answered by the
 * session cookie without the user typing anything — prompting there is what
 * made the offer feel like a nag on every visit.
 *
 * So both must hold: this page load follows the redirect
 * (`consumeFreshAuthentication`), and the session behind the token was itself
 * authenticated seconds ago (`isRecentAuthentication`, which reads `auth_time`
 * — old on a cookie resume, now on a password login or a sign-up). Someone
 * already signed in gets nothing; the account menu still offers enrollment on
 * demand.
 *
 * Three things settle the campaign without ever showing the modal: returning
 * from the identity provider with a `kc_action_status` (the user just answered
 * the offer — `success` records `done`, `cancelled`/`error` records `skipped`),
 * a page load that is not a fresh sign-in, and another campaign sitting at
 * `seen`, which means its Joyride tour is mid-flight and a modal would bury the
 * spotlight.
 */
export function PasskeyPrompt({ onOpenChange }: { onOpenChange?: (open: boolean) => void }) {
  const { token } = useAuth();
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

    if (!consumeFreshAuthentication()) return;
    if (!isRecentAuthentication(token)) return;

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
        }, SETTLE_MS);
      })
      .catch(() => {});

    return () => {
      alive = false;
      if (timer) clearTimeout(timer);
    };
  }, [report, token]);

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

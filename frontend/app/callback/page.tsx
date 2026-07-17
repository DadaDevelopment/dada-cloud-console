"use client";
/**
 * OIDC redirect callback. `@dada/react-sso`'s oidcProvider.load() handles
 * signinRedirectCallback automatically when it detects code= in the URL,
 * replacing the URL with the returnTo path carried in the OIDC `state` (via
 * window.history.replaceState) before flipping to authenticated. That history
 * change lands on this same route component without a client navigation, so
 * once authenticated this reads the URL the library already rewrote and
 * navigates there; falls back to /projects for the redirect_uri's own
 * pathname (no returnTo was set).
 */
import { useEffect, useRef } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth";
import { Spinner } from "@/components/ui/spinner";
import { PENDING_REGISTRATION_KEY } from "@/lib/register-redirect";

/**
 * How long a {@link PENDING_REGISTRATION_KEY} marker stays valid. Bounds the
 * window so a stale marker (user abandoned sign-up, came back days later via
 * a plain login) can't misfire `registration_complete`.
 */
const REGISTRATION_WINDOW_MS = 30 * 60 * 1000;

export default function CallbackPage() {
  const router = useRouter();
  const { token, isLoading } = useAuth();
  const registrationGoalCheckedRef = useRef(false);

  useEffect(() => {
    if (!isLoading && token) {
      const target = window.location.pathname + window.location.search;
      router.replace(target === "/callback" ? "/projects" : target);
    }
  }, [isLoading, token, router]);

  /**
   * Fires the `registration_complete` Metrika goal the moment a Keycloak
   * sign-up actually completes — the existing `register` goal only measures
   * reaching /register (intent), so it overcounts against real signups.
   * Guarded by consuming {@link PENDING_REGISTRATION_KEY}, which `startRegister`
   * sets right before the redirect: only present here if this authenticated
   * return came from the sign-up flow, and only honored within
   * {@link REGISTRATION_WINDOW_MS} of being set.
   */
  useEffect(() => {
    if (isLoading || !token) return;
    if (registrationGoalCheckedRef.current) return;
    registrationGoalCheckedRef.current = true;
    try {
      const pending = window.localStorage.getItem(PENDING_REGISTRATION_KEY);
      window.localStorage.removeItem(PENDING_REGISTRATION_KEY);
      if (!pending) return;
      const startedAt = Number(pending);
      if (!Number.isFinite(startedAt) || Date.now() - startedAt > REGISTRATION_WINDOW_MS) return;
      (window as { ym?: (id: number, action: string, target: string) => void }).ym?.(
        110158915,
        "reachGoal",
        "registration_complete",
      );
    } catch {}
  }, [isLoading, token]);

  return (
    <div className="flex h-screen items-center justify-center bg-gray-50">
      <Spinner size="lg" />
    </div>
  );
}
